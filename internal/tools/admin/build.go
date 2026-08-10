package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildstatus"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type buildSiteInput struct{}

// buildSiteData is the canonical data.* payload (#572): build_site was the
// last tool with zero envelope (no data/errors/meta/success at all, not
// even root-level duplication). Root fields below are kept as
// compatibility aliases, additive only — same treatment #552 gave
// create_preview/generate_hero_image/etc.
type buildSiteData struct {
	Status         string `json:"status"`
	DurationMs     int64  `json:"duration_ms"`
	BuildID        string `json:"build_id"`
	OutputRevision string `json:"output_revision,omitempty"`
	PublishReady   bool   `json:"publish_ready"`
	Warning        string `json:"warning,omitempty"`
	// Stages / Pages are additive, stage-aware and page-aware reporting
	// (#858). Every pre-existing field above is preserved unchanged for
	// backward compatibility; these are new nested objects consumers may
	// ignore. Both are always populated (Stages unconditionally; Pages'
	// slices default to empty, never null).
	Stages *buildStagesDTO `json:"stages,omitempty"`
	Pages  *buildPagesDTO  `json:"pages,omitempty"`
}

// buildStagesDTO breaks a single pass/fail into the distinct internal stages
// #858 AC2 asks for. A Hugo build failure never reaches this struct (it
// surfaces as a tool error before post-build), so HugoBuild is "ok" whenever
// stages are reported at all; it is included for completeness and symmetry.
// SourceIndexReload / PublicIndexReload are both driven by the single
// "index_reload" post-build callback (which reloads the public site index and
// then the source index); when that callback is absent they report "skipped".
// Callbacks carries every named post-build callback's individual outcome
// (ok/failed/timeout), and CallbacksStatus summarises them.
type buildStagesDTO struct {
	HugoBuild         string            `json:"hugo_build"`
	OutputSwap        string            `json:"output_swap"`
	SourceIndexReload string            `json:"source_index_reload"`
	PublicIndexReload string            `json:"public_index_reload"`
	Callbacks         map[string]string `json:"callbacks,omitempty"`
	CallbacksStatus   string            `json:"callbacks_status"`
}

// buildPagesDTO is the page-aware view of the *changed set* for this build —
// the source pages an MCP mutation marked pending since the last build, split
// by whether they were included in the published output or excluded as drafts
// (#858 AC1/AC3). Identifiers are "slug" or "slug:lang". DeletedOutputs is
// populated best-effort: Hugo's --cleanDestinationDir removes outputs of
// deleted sources but does not report which, so per-page deletion tracking is
// not yet wired and this stays empty (documented scope limit, see PR body).
type buildPagesDTO struct {
	Included       []string `json:"included"`
	ExcludedDrafts []string `json:"excluded_drafts"`
	DeletedOutputs []string `json:"deleted_outputs"`
}

type buildSiteOutput struct {
	toolcontract.ToolResponse[buildSiteData]
	Status         string `json:"status"`
	DurationMs     int64  `json:"duration_ms"`
	BuildID        string `json:"build_id"`
	OutputRevision string `json:"output_revision,omitempty"`
	PublishReady   bool   `json:"publish_ready"`
	Warning        string `json:"warning,omitempty"`
}

func buildSiteSuccessEnvelope[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

func buildSiteSuccessEnvelopeWithWarning[T any](data T, warning string) toolcontract.ToolResponse[T] {
	envelope := buildSiteSuccessEnvelope(data)
	if strings.TrimSpace(warning) != "" {
		envelope.Warnings = []string{warning}
	}
	return envelope
}

func newBuildSiteOutput(data buildSiteData) buildSiteOutput {
	return buildSiteOutput{
		ToolResponse:   buildSiteSuccessEnvelopeWithWarning(data, data.Warning),
		Status:         data.Status,
		DurationMs:     data.DurationMs,
		BuildID:        data.BuildID,
		OutputRevision: data.OutputRevision,
		PublishReady:   data.PublishReady,
		Warning:        data.Warning,
	}
}

// buildErrorPayload is the structured JSON returned on Hugo failure.
type buildErrorPayload struct {
	Error            string `json:"error"`
	ErrorClass       string `json:"error_class,omitempty"`
	ExitCode         int    `json:"exit_code"`
	Command          string `json:"command"`
	WorkingDirectory string `json:"working_directory"`
	CacheDirectory   string `json:"cache_directory,omitempty"`
	DurationMs       int64  `json:"duration_ms"`
	StderrSummary    string `json:"stderr_summary"`
	StdoutSummary    string `json:"stdout_summary,omitempty"`
	BuildID          string `json:"build_id"`
	LogHint          string `json:"log_hint"`
	Suggestion       string `json:"suggestion,omitempty"`
	DocsURL          string `json:"docs_url,omitempty"`
}

// buildPreflightPayload is the structured JSON returned when a pre-flight check fails.
type buildPreflightPayload struct {
	Error        string `json:"error"`
	ErrorClass   string `json:"error_class"`
	Path         string `json:"path"`
	OperatorHint string `json:"operator_hint"`
	Suggestion   string `json:"suggestion"`
	DocsURL      string `json:"docs_url"`
	Retryable    bool   `json:"retryable"`
}

const buildDocsURL = "docs/operator-guide.md#build-permissions"

// checkBuildWritable verifies that the directories Hugo must write to are
// accessible before invoking the build. Returns a structured JSON error on
// the first problematic path found.
//
// Two checks per directory:
//  1. os.CreateTemp — confirms write permission (ReadWritePaths configured)
//  2. directory uid == euid — Hugo calls chtimes on pre-existing files it
//     copies into the output directory; POSIX requires ownership (not just
//     write) for non-NULL timestamps. A dir owned by a different user means
//     its existing files will trigger EPERM on chtimes.
func checkBuildWritable(paths ...string) error {
	euid := os.Geteuid()
	for _, dir := range paths {
		if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- these are configured service-owned build paths
			return buildPreflightError(dir)
		}
		f, err := os.CreateTemp(dir, ".mcp-preflight-*.tmp")
		if err != nil {
			return buildPreflightError(dir)
		}
		_ = f.Close()
		_ = os.Remove(f.Name())
		// Check ownership: chtimes on pre-existing files requires the process
		// to own them. If the directory itself belongs to a different uid,
		// its pre-existing files are almost certainly owned by that uid too.
		fi, statErr := os.Stat(dir)
		if statErr != nil {
			return buildPreflightError(dir)
		}
		if ownershipMismatch(fi, euid) {
			return buildPreflightChownError(dir)
		}
	}
	return nil
}

func buildPreflightError(dir string) error {
	payload := buildPreflightPayload{
		Error:        "build_precondition_failed",
		ErrorClass:   "permission_denied",
		Path:         dir,
		OperatorHint: "Add this path to ReadWritePaths in the systemd service override and reload: sudo systemctl daemon-reload && sudo systemctl restart mcp-hugo-server-go",
		Suggestion:   "Check that the MCP service user owns or has write access to this directory, and that it is listed in ReadWritePaths in the systemd service.",
		DocsURL:      buildDocsURL,
		Retryable:    false,
	}
	b, _ := json.Marshal(payload)
	return fmt.Errorf("build_precondition_failed: %s", b)
}

func buildPreflightChownError(dir string) error {
	payload := buildPreflightPayload{
		Error:        "build_precondition_failed",
		ErrorClass:   "permission_denied",
		Path:         dir,
		OperatorHint: "Run: sudo chown -R $(systemctl show mcp-hugo-server-go -p User --value) " + dir + " && sudo systemctl restart mcp-hugo-server-go",
		Suggestion:   "The MCP service user can write to this directory but does not own it. Hugo requires ownership to set file timestamps (chtimes). Change ownership to the service user.",
		DocsURL:      buildDocsURL,
		Retryable:    false,
	}
	b, _ := json.Marshal(payload)
	return fmt.Errorf("build_precondition_failed: %s", b)
}

// newBuildID generates a build identifier of the form YYYYMMDD-HHMMSS-<4 random lowercase hex chars>.
func newBuildID(t time.Time) string {
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	return t.UTC().Format("20060102-150405") + "-" + fmt.Sprintf("%04x", b)
}

// truncateUTF8 returns a string from b that is at most maxBytes bytes and ends
// on a valid UTF-8 boundary.
func truncateUTF8(b []byte, maxBytes int) string {
	if len(b) <= maxBytes {
		return string(b)
	}
	b = b[:maxBytes]
	// Walk back continuation bytes (0x80–0xBF).
	for len(b) > 0 && b[len(b)-1]&0xC0 == 0x80 {
		b = b[:len(b)-1]
	}
	// Remove a stranded leading byte (0xC0–0xFF) left by the walk-back.
	if len(b) > 0 && b[len(b)-1]&0xC0 == 0xC0 {
		b = b[:len(b)-1]
	}
	return string(b)
}

// sanitiseStderr redacts absolute paths in raw stderr, then truncates to 500
// bytes on a valid UTF-8 boundary. Sanitisation happens before truncation so
// that paths near the 500-byte limit are always redacted.
func sanitiseStderr(raw []byte, hugoRoot, siteRoot string) string {
	s := string(raw)
	if hugoRoot != "" {
		s = strings.ReplaceAll(s, hugoRoot, "<site_root>")
	}
	if siteRoot != "" && siteRoot != hugoRoot {
		s = strings.ReplaceAll(s, siteRoot, "<site_root>")
	}
	return strings.TrimSpace(truncateUTF8([]byte(s), 500))
}

func buildOutputSummary(stderr, stdout []byte, hugoRoot, siteRoot string) string {
	if strings.TrimSpace(string(stderr)) != "" {
		return sanitiseStderr(stderr, hugoRoot, siteRoot)
	}
	return sanitiseStderr(stdout, hugoRoot, siteRoot)
}

func outputTail(raw []byte, hugoRoot, siteRoot string) string {
	return sanitiseStderr(raw, hugoRoot, siteRoot)
}

func hugoCacheDir(cfg config.Config) string {
	if p := strings.TrimSpace(cfg.OAuth.StoragePath); p != "" {
		return filepath.Join(filepath.Dir(p), "hugo-cache")
	}
	return filepath.Join(os.TempDir(), "mcp-hugo-server-go", "hugo-cache")
}

func buildCommandArgs(cacheDir string, preview bool) []string {
	args := []string{"--noBuildLock", "--cacheDir", cacheDir}
	if preview {
		args = append(args, "--renderToMemory")
	} else {
		// Without this flag Hugo never removes output files whose source
		// page was deleted since the last build; they linger in site_root
		// indefinitely (#524).
		args = append(args, "--cleanDestinationDir")
	}
	return args
}

// swapBuildOutput installs a fully rendered Hugo destination only after Hugo
// has completed successfully. The old output is retained until the new tree
// is in place; if the second rename fails, it is restored. The returned
// warning is non-fatal because the public tree is already the new build and
// only cleanup of the backup failed (#965).
func swapBuildOutput(tempDir, siteRoot string) (string, error) {
	parent := filepath.Dir(siteRoot)
	backup, err := os.MkdirTemp(parent, ".mcp-public-backup-")
	if err != nil {
		return "", fmt.Errorf("output_swap: failed to prepare backup directory")
	}
	if err := os.Remove(backup); err != nil {
		return "", fmt.Errorf("output_swap: failed to prepare backup path")
	}

	hadOld := false
	if _, err := os.Lstat(siteRoot); err == nil {
		if err := os.Rename(siteRoot, backup); err != nil {
			return "", fmt.Errorf("output_swap: failed to stage existing public output")
		}
		hadOld = true
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("output_swap: failed to inspect existing public output")
	}

	if err := os.Rename(tempDir, siteRoot); err != nil {
		if hadOld {
			_ = os.Rename(backup, siteRoot)
		}
		return "", fmt.Errorf("output_swap: failed to install rendered output")
	}
	if !hadOld {
		return "", nil
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Sprintf("output_swap: new output installed but previous output cleanup failed: %v", err), nil
	}
	return "", nil
}

func commandString(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

func currentUserForLog() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}

func hashTree(root string) (string, error) {
	h := sha256.New()
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			rel = ""
		}
		if _, err := io.WriteString(h, rel); err != nil {
			return err
		}
		if info.IsDir() {
			_, err = io.WriteString(h, "\nD\n")
			return err
		}
		if _, err := io.WriteString(h, "\nF\n"); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		_, err = io.WriteString(h, "\n")
		return err
	}); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func boundedCommandEnv() []string {
	env := make([]string, 0, 5)
	if path := os.Getenv("PATH"); path != "" {
		env = append(env, "PATH="+path)
	}
	if home := os.Getenv("HOME"); home != "" {
		env = append(env, "HOME="+home)
	}
	if lang := os.Getenv("LANG"); lang != "" {
		env = append(env, "LANG="+lang)
	}
	if lcAll := os.Getenv("LC_ALL"); lcAll != "" {
		env = append(env, "LC_ALL="+lcAll)
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	return env
}

func classifyBuildFailure(ctx context.Context, summary string) string {
	switch {
	case ctx.Err() != nil:
		return "timeout"
	case strings.Contains(strings.ToLower(summary), "permission denied"),
		strings.Contains(strings.ToLower(summary), "read-only file system"),
		strings.Contains(strings.ToLower(summary), "operation not permitted"):
		return "permission_denied"
	default:
		return "build_error"
	}
}

func ownershipDriftSuggestion(summary string) (string, bool) {
	lower := strings.ToLower(summary)
	if !strings.Contains(lower, "chtimes ") || !strings.Contains(lower, "operation not permitted") {
		return "", false
	}
	return "A file in the build output is likely owned by a different local user than the MCP service account. Inspect the reported path under public/ or resources/, fix ownership, then rerun build_site.", true
}

// PostBuildCallback pairs a post-build side-effect function with a stable,
// human-readable name (#644). A failure/timeout warning previously
// identified the responsible callback only by its positional index in the
// server.go wiring order ("post-build callback 2 timed out") — meaningless
// to a caller or operator without reading that source file. Naming each
// callback lets publish_changes's data.build.warning point directly at
// which post-build step (index reload, DB reindex, CDN purge, search
// engine submission, ...) is broken, closing the observability gap #644
// reported: publish_changes already knows *that* something failed, but
// nothing downstream could say *what*.
type PostBuildCallback struct {
	Name string
	Fn   func() error
}

func RegisterBuild(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, siteReload ...PostBuildCallback) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:         "build_site",
		Title:        "Build website",
		Description:  "Build the Hugo site and return the build duration in milliseconds. Use this after content changes or before publishing. Returns build_in_progress if another build or content mutation is active. Response is stage-aware (`data.stages`: hugo_build, output_swap, source/public index reload, per-callback outcomes) and page-aware (`data.pages`: which changed translations were included vs excluded_drafts) — all additive to the pre-existing fields (#858).",
		InputSchema:  tools.MustSchema[buildSiteInput](),
		OutputSchema: tools.MustSchema[buildSiteOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ buildSiteInput) (*mcp.CallToolResult, buildSiteOutput, error) {
		data, err := runBuild(ctx, cfg, srcIdx, siteReload...)
		if err != nil {
			return nil, buildSiteOutput{}, err
		}
		return nil, newBuildSiteOutput(data), nil
	}))
}

// sourceKeyOf formats a changed-page identifier as "slug" or "slug:lang"
// (#858 example shape, e.g. "posts/example:fr").
func sourceKeyOf(slug, lang string) string {
	if strings.TrimSpace(lang) == "" {
		return slug
	}
	return slug + ":" + lang
}

// classifyPendingPages splits the pending (changed) source pages into the
// published set and the draft/test-content-excluded set, sorted and
// deduplicated. A page is excluded when draft:true or test_content is truthy.
func classifyPendingPages(pending []hugosite.SourcePage) buildPagesDTO {
	included := []string{}
	excluded := []string{}
	for _, p := range pending {
		key := sourceKeyOf(p.Slug, p.Lang)
		if p.Draft || isTruthyFrontmatter(p.FrontmatterRaw["test_content"]) {
			excluded = append(excluded, key)
		} else {
			included = append(included, key)
		}
	}
	sort.Strings(included)
	sort.Strings(excluded)
	return buildPagesDTO{
		Included:       included,
		ExcludedDrafts: excluded,
		DeletedOutputs: []string{},
	}
}

func isTruthyFrontmatter(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true")
	default:
		return false
	}
}

// runBuild performs an actual Hugo build and its post-build callbacks —
// the same logic build_site's own handler runs, extracted so publish_changes
// (#340, #438) can drive a build without going through the MCP tool
// dispatch layer a second time. Takes and releases hugosite.ContentMu itself,
// so callers must not already hold it.
func runBuild(ctx context.Context, cfg config.Config, srcIdx *hugosite.SourceIndex, siteReload ...PostBuildCallback) (buildSiteData, error) {
	if cfg.HugoRoot == "" {
		return buildSiteData{}, fmt.Errorf("config_error: hugo_root is not configured")
	}
	if !hugosite.ContentMu.TryLock() {
		return buildSiteData{}, fmt.Errorf("build_in_progress: a content mutation or build is already running")
	}
	defer hugosite.ContentMu.Unlock()

	if err := checkBuildWritable(filepath.Dir(cfg.SiteRoot), filepath.Join(cfg.HugoRoot, "resources")); err != nil {
		buildstatus.RecordFailure("permission_denied", time.Now())
		return buildSiteData{}, err
	}
	buildDir, buildDirErr := os.MkdirTemp(filepath.Dir(cfg.SiteRoot), ".mcp-build-output-")
	if buildDirErr != nil {
		buildstatus.RecordFailure("permission_denied", time.Now())
		return buildSiteData{}, buildPreflightError(filepath.Dir(cfg.SiteRoot))
	}
	defer func() { _ = os.RemoveAll(buildDir) }()

	timeout := cfg.BuildTimeoutSeconds
	if timeout <= 0 {
		timeout = 120
	}
	cacheDir := hugoCacheDir(cfg)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil { // #nosec G301 -- Hugo cache is a configured service-owned path
		return buildSiteData{}, fmt.Errorf("config_error: failed to prepare Hugo cache directory")
	}
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	start := time.Now()
	runID := newBuildID(start)
	args := append(buildCommandArgs(cacheDir, false), "--destination", buildDir)
	// #nosec G204 -- executable is fixed to hugo; args come from
	// buildCommandArgs and validated config, not from MCP caller input.
	cmd := exec.CommandContext(tctx, "hugo", args...)
	cmd.Dir = cfg.HugoRoot
	cmd.Env = boundedCommandEnv()
	setNewProcessGroup(cmd)
	// Kill the whole process group on timeout/cancellation so that shell
	// wrappers and any children spawned by hugo are also terminated (#240/#243).
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
	var stderrBuf bytes.Buffer
	var stdoutBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	cmd.Stdout = &stdoutBuf
	err := cmd.Run()
	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil {
		summary := buildOutputSummary(stderrBuf.Bytes(), stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot)
		errClass := classifyBuildFailure(tctx, summary)
		slog.Error("build_site failed",
			"build_id", runID,
			"tool", "build_site",
			"user", currentUserForLog(),
			"command", commandString("hugo", args),
			"cwd", cfg.HugoRoot,
			"cache_dir", cacheDir,
			"duration_ms", durationMs,
			"exit_code", exitCode,
			"error_class", errClass,
			"stdout_tail", outputTail(stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
			"stderr_tail", outputTail(stderrBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
			"output_summary", summary,
			"error", err,
		)

		errKind := "build_error"
		if tctx.Err() != nil {
			errKind = "build_timeout"
		}

		payload := buildErrorPayload{
			Error:            errKind,
			ErrorClass:       errClass,
			ExitCode:         exitCode,
			Command:          commandString("hugo", args),
			WorkingDirectory: cfg.HugoRoot,
			CacheDirectory:   cacheDir,
			DurationMs:       durationMs,
			StderrSummary:    summary,
			StdoutSummary:    outputTail(stdoutBuf.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
			BuildID:          runID,
			LogHint:          "Search server logs for build_id=" + runID,
		}
		if errClass == "permission_denied" {
			if suggestion, ok := ownershipDriftSuggestion(summary); ok {
				payload.Suggestion = suggestion
			} else {
				payload.Suggestion = "Verify that site_root and hugo_root/resources are listed in ReadWritePaths in the systemd service override. Run: systemctl cat mcp-hugo-server-go"
			}
			payload.DocsURL = buildDocsURL
		}
		jsonPayload, _ := json.Marshal(payload)
		buildstatus.RecordFailure(errClass, time.Now())
		return buildSiteData{}, fmt.Errorf("build_error: %s", jsonPayload)
	}
	buildstatus.RecordSuccess(time.Now())
	swapWarning, swapErr := swapBuildOutput(buildDir, cfg.SiteRoot)
	if swapErr != nil {
		buildstatus.RecordFailure("output_swap", time.Now())
		return buildSiteData{}, swapErr
	}

	// Capture the page-aware "changed set" BEFORE the callback loop runs: the
	// index_reload callback calls ClearAllBuildPending(), so pending pages
	// must be snapshotted here or they vanish before we can report them (#858).
	pages := classifyPendingPages(srcIdx.PendingPages())

	// Run post-build callbacks within a bounded deadline (#241). Optional
	// side-effect callbacks (CDN purge, search indexing) swallow their errors
	// at the call site in server.go; any error here means a required step
	// (index reload, DB sync) failed. Surface as partial_success so callers
	// know the build succeeded but read state may be stale (#238/#244).
	const callbackTimeout = 30 * time.Second
	cbCtx, cbCancel := context.WithTimeout(context.Background(), callbackTimeout)
	defer cbCancel()
	var cbWarning string
	if swapWarning != "" {
		cbWarning = swapWarning
	}
	// callbackOutcomes records each named callback's individual result for the
	// stage-aware report (#858 AC2). Any callback not reached (because an
	// earlier one timed out and broke the loop) is left absent, then filled in
	// as "skipped" below.
	callbackOutcomes := map[string]string{}
cbLoop:
	for i, cb := range siteReload {
		if cb.Fn == nil {
			continue
		}
		name := cb.Name
		if name == "" {
			name = fmt.Sprintf("callback %d", i)
		}
		done := make(chan error, 1)
		go func(f func() error) { done <- f() }(cb.Fn)
		select {
		case cbErr := <-done:
			if cbErr != nil {
				callbackOutcomes[name] = "failed"
				cbWarning = fmt.Sprintf("post-build callback %q failed: %v", name, cbErr)
				slog.Warn("build_site: post-build callback failed", "callback", name, "error", cbErr)
			} else {
				callbackOutcomes[name] = "ok"
			}
		case <-cbCtx.Done():
			callbackOutcomes[name] = "timeout"
			cbWarning = fmt.Sprintf("post-build callback %q timed out after %s", name, callbackTimeout)
			slog.Warn("build_site: post-build callback timed out", "callback", name, "timeout", callbackTimeout)
			break cbLoop
		}
	}
	// Mark registered-but-not-reached callbacks (post-break) as skipped.
	for _, cb := range siteReload {
		if cb.Fn == nil || cb.Name == "" {
			continue
		}
		if _, seen := callbackOutcomes[cb.Name]; !seen {
			callbackOutcomes[cb.Name] = "skipped"
		}
	}
	stages := buildStages(callbackOutcomes)

	status := "ok"
	if cbWarning != "" {
		status = "partial_success"
	}
	outputRevision, hashErr := hashTree(cfg.SiteRoot)
	if hashErr != nil {
		slog.Warn("build_site: failed to hash output tree", "error", hashErr)
		if cbWarning == "" {
			cbWarning = "output revision unavailable after build"
		} else {
			cbWarning += "; output revision unavailable after build"
		}
		status = "partial_success"
	}
	publishReady := status == "ok"
	slog.Info("build_site completed",
		"build_id", runID,
		"tool", "build_site",
		"user", currentUserForLog(),
		"command", commandString("hugo", args),
		"cwd", cfg.HugoRoot,
		"cache_dir", cacheDir,
		"duration_ms", durationMs,
		"exit_code", exitCode,
		"status", status,
		"publish_ready", publishReady,
	)
	// output_swap reflects whether the built output is in place on disk. Hugo
	// writes public/ in place with --cleanDestinationDir (there is no separate
	// atomic swap stage in this deployment); a failure to hash the tree is the
	// only signal here that the output is not readable, so mirror that.
	if hashErr != nil {
		stages.OutputSwap = "degraded"
	}
	return buildSiteData{
		Status:         status,
		DurationMs:     durationMs,
		BuildID:        runID,
		OutputRevision: outputRevision,
		PublishReady:   publishReady,
		Warning:        cbWarning,
		Stages:         &stages,
		Pages:          &pages,
	}, nil
}

// buildStages maps the individual post-build callback outcomes onto the
// stage-aware report (#858 AC2). The "index_reload" callback drives both the
// source and public index-reload stages (it reloads the public site index
// then the source index); when absent, both are "skipped". CallbacksStatus
// summarises every callback: "ok" if all succeeded, "skipped" if none ran,
// "partial_failure" if any failed or timed out.
func buildStages(callbackOutcomes map[string]string) buildStagesDTO {
	indexReload := "skipped"
	if v, ok := callbackOutcomes["index_reload"]; ok {
		indexReload = v
	}
	callbacksStatus := "skipped"
	if len(callbackOutcomes) > 0 {
		callbacksStatus = "ok"
		for _, outcome := range callbackOutcomes {
			if outcome == "failed" || outcome == "timeout" {
				callbacksStatus = "partial_failure"
				break
			}
		}
	}
	var callbacks map[string]string
	if len(callbackOutcomes) > 0 {
		callbacks = callbackOutcomes
	}
	return buildStagesDTO{
		HugoBuild:         "ok",
		OutputSwap:        "ok",
		SourceIndexReload: indexReload,
		PublicIndexReload: indexReload,
		Callbacks:         callbacks,
		CallbacksStatus:   callbacksStatus,
	}
}
