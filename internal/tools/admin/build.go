package admin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	gobuildinfo "debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/changeset"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type buildSiteInput struct {
	// ChangeSetID scopes #1140's foreign-change-set guard: if any page
	// pending a build is known (to this same running process) to belong to
	// a change-set the caller hasn't acknowledged, the build is refused
	// with foreign_change_set_present rather than silently publishing
	// another change-set's in-flight edits. Omitting this uses the
	// caller's implicit per-principal default change-set, matching every
	// mutation tool's own omitted-change_set_id behavior (#1135).
	ChangeSetID string `json:"change_set_id,omitempty"`
	// ChangeSetIDs acknowledges multiple change-sets at once — required
	// the moment two or more change-sets genuinely have pending work
	// simultaneously (the normal state once two agents are actually
	// editing concurrently), since a single change_set_id can never
	// account for another change-set's real pending page. If both this
	// and ChangeSetID are set, ChangeSetIDs wins. Every id is resolved and
	// ownership-checked individually.
	ChangeSetIDs []string `json:"change_set_ids,omitempty"`
	// ExpectedSourceRevision/ExpectedPublicRevision (#1141) are optional
	// optimistic-concurrency guards: pass a revision previously read from
	// get_runtime_status (include_revisions:true) to have this build
	// refuse with source_revision_conflict/public_revision_conflict if the
	// tree has since changed, rather than silently building/promoting over
	// a change you never saw. See runBuild's own doc comment for the full
	// TOCTOU discussion, including the independent (always-on) check for
	// source drift during the build itself.
	ExpectedSourceRevision string `json:"expected_source_revision,omitempty"`
	ExpectedPublicRevision string `json:"expected_public_revision,omitempty"`
}

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
	// SourceRevision (#1141) is the content_root hash this build verified
	// and built against (empty when content_root isn't configured) — the
	// same hashTree value get_runtime_status's include_revisions option
	// reports, and what a caller can pass back as expected_source_revision
	// on a later call. publish_changes also uses this internally to detect
	// source drift during its post-build verify_publication poll (see its
	// own handler).
	SourceRevision string `json:"source_revision,omitempty"`
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

// checkDirWritable verifies only that a directory is writable (via a real
// CreateTemp probe), without requiring the process to own it. Use this for
// directories Hugo never chtimes pre-existing files inside directly — e.g.
// the parent of SiteRoot, which #965's atomic build only needs write access
// to in order to create sibling .mcp-build-output-*/.mcp-public-backup-*
// temp directories. Those temp directories are created by this process and
// are therefore always owned by it, regardless of who owns the parent.
func checkDirWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- these are configured service-owned build paths
		return buildPreflightError(dir)
	}
	f, err := os.CreateTemp(dir, ".mcp-preflight-*.tmp")
	if err != nil {
		return buildPreflightError(dir)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return nil
}

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
//
// Only call this on directories Hugo actually reuses/rewrites pre-existing
// files inside across builds (e.g. the resources cache) — not on a
// directory this process merely needs to create fresh siblings under; use
// checkDirWritable for that (#981 production incident: the parent of
// SiteRoot passed the writability probe but is legitimately owned by a
// different operator account, which incorrectly failed preflight here even
// though nothing under it gets chtimes'd directly).
// geteuid is a test seam: real UID mismatches can't be reproduced in CI
// without root, so tests override this to exercise ownershipMismatch
// deterministically.
var geteuid = os.Geteuid

func checkBuildWritable(paths ...string) error {
	euid := geteuid()
	for _, dir := range paths {
		if err := checkDirWritable(dir); err != nil {
			return err
		}
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
	return swapBuildOutputWithOps(tempDir, siteRoot, os.Rename, os.RemoveAll)
}

// swapBuildOutputWithOps keeps the destructive boundaries injectable without
// mutable package globals. Production supplies os.Rename/os.RemoveAll; tests
// can deterministically interrupt the install and cleanup steps.
func swapBuildOutputWithOps(tempDir, siteRoot string, rename func(string, string) error, removeAll func(string) error) (string, error) {
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
		if err := rename(siteRoot, backup); err != nil {
			return "", fmt.Errorf("output_swap: failed to stage existing public output")
		}
		hadOld = true
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("output_swap: failed to inspect existing public output")
	}

	if err := rename(tempDir, siteRoot); err != nil {
		if hadOld {
			_ = rename(backup, siteRoot)
		}
		return "", fmt.Errorf("output_swap: failed to install rendered output")
	}
	if !hadOld {
		return "", nil
	}
	if err := removeAll(backup); err != nil {
		return fmt.Sprintf("output_swap: new output installed but previous output cleanup failed: %v", err), nil
	}
	return "", nil
}

// maxUnreadableOutputPathsReported bounds how many still-unfixable paths
// fixUnreadableOutputPaths reports before stopping, so a pathological case
// (e.g. an entire tree owned by a different uid) still returns quickly and
// produces a usable, not overwhelming, diagnostic.
const maxUnreadableOutputPathsReported = 20

// fixUnreadableOutputPaths walks root and, for every directory missing the
// "other" execute bit or file missing the "other" read bit, attempts to add
// it via os.Chmod. That permission shape is exactly what makes a path
// invisible to a reverse proxy running as a different uid/gid than the
// build process, even though the file was written successfully and the
// build itself reports success.
//
// Every path under root was just written by this same process as part of
// this same build, so correcting a permission bit here is not new
// privilege — it is finishing a write this process already owns (the same
// reasoning as swapBuildOutput's own chmod on the top-level directory).
// Chmod can still legitimately fail — e.g. a file this process does not
// itself own, left behind by something else — and those genuine failures
// are what gets returned, capped at maxUnreadableOutputPathsReported, so an
// operator only ever hears about problems that actually need their
// attention rather than ones this process already fixed silently.
//
// Walk/stat errors are otherwise ignored: this is a best-effort safety net
// layered on top of the real fix in swapBuildOutput, not a build-blocking
// check, and must never itself turn a successful build into a failure.
func fixUnreadableOutputPaths(root string) []string {
	var stillBad []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if len(stillBad) >= maxUnreadableOutputPathsReported {
			return filepath.SkipAll
		}
		if err != nil {
			return nil
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		mode := fi.Mode().Perm()
		want := os.FileMode(0o004) // other:r--
		if d.IsDir() {
			want = 0o005 // other:r-x, needed to traverse/list a directory
		}
		if mode&want == want {
			return nil
		}
		if chmodErr := os.Chmod(path, mode|want); chmodErr != nil {
			stillBad = append(stillBad, fmt.Sprintf("%s (mode %04o): %v", path, mode, chmodErr))
		}
		return nil
	})
	return stillBad
}

func commandString(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

// hugoVersionFromBinary reads Go module metadata from the resolved Hugo
// executable without launching a second process. That keeps build lifecycle
// accounting truthful while avoiding side effects and process-group leaks from
// operator-supplied shell wrappers named `hugo`.
func hugoVersionFromBinary() string {
	path, err := exec.LookPath("hugo")
	if err != nil {
		return ""
	}
	info, err := gobuildinfo.ReadFile(path)
	if err != nil {
		return ""
	}
	return formatHugoBuildVersion(info)
}

func formatHugoBuildVersion(info *gobuildinfo.BuildInfo) string {
	if info == nil || info.Main.Path != "github.com/gohugoio/hugo" {
		return ""
	}
	version := strings.TrimPrefix(strings.TrimSpace(info.Main.Version), "v")
	if version == "" || version == "(devel)" {
		return ""
	}
	extended := false
	for _, setting := range info.Settings {
		if setting.Key == "-tags" {
			for _, tag := range strings.Split(setting.Value, ",") {
				if strings.TrimSpace(tag) == "extended" {
					extended = true
				}
			}
		}
	}
	if extended && !strings.Contains(version, "+extended") {
		version += "+extended"
	}
	return version
}

func currentUserForLog() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}

// hashTreeExcludedNames are files hashTree skips even though they live
// inside the tree it walks. ".mcp-audit.log" (written by delete_page/
// delete_page_asset directly under ContentRoot) is MCP-internal bookkeeping,
// not site content: it grows on every mutation, including ones a caller
// later reverses, so a create->modify->delete round-trip that leaves every
// *.md source page byte-identical to baseline still left new lines appended
// to this log — making hashTree's result (source_revision/public_revision)
// falsely report content drift after a true no-op cycle (#1180). Excluding
// it here keeps source_revision deterministic over the site's actual page
// content, matching what publish_changes/build_site treat as the source of
// truth for drift detection.
var hashTreeExcludedNames = map[string]bool{
	".mcp-audit.log": true,
}

// hashTreeExcludedPrefix skips any file whose name starts with this prefix,
// for names that vary per-instance (so an exact-name map in
// hashTreeExcludedNames can't cover them) and are never site content.
// fileutil.UploadStagingPrefix (".upload-<upload_id>.part", #1196's
// chunked-upload staging file) is the only current case: it lives inside a
// live bundle directory for up to 15 minutes while a commit is in flight,
// or longer if abandoned until the startup sweep clears it, and would
// otherwise shift source_revision the exact same way .mcp-audit.log did
// before #1180. Sourced from fileutil rather than internal/tools/write
// directly — write already imports admin, so admin importing write back
// would cycle; fileutil is the shared low-level package both sides can
// reference instead of duplicating the literal (see fileutil's own doc
// comment on UploadStagingPrefix).
const hashTreeExcludedPrefix = fileutil.UploadStagingPrefix

func hashTree(root string) (string, error) {
	h := sha256.New()
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if hashTreeExcludedNames[info.Name()] || strings.HasPrefix(info.Name(), hashTreeExcludedPrefix) {
			return nil
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
	// OnBuildPrepared runs after the source index has been deterministically
	// reloaded from disk but before Hugo starts. It may persist per-page build
	// intent and return the durable changed/deleted set for reporting.
	OnBuildPrepared func(BuildProgress) ([]BuildPageChange, error)
	// OnBuildFailed finalizes durable build intent after Hugo/output-swap
	// failure. A process crash intentionally leaves the run in_progress for
	// startup recovery instead.
	OnBuildFailed func(BuildProgress, string) error
	// OnBuildStart runs while ContentMu is held, immediately before Hugo is
	// invoked. It is for durable intent records: a database transaction cannot
	// include the subsequent filesystem rename, so recovery needs this fact
	// before the external process has a chance to write output.
	OnBuildStart func(BuildProgress) error
	// OnOutputSwapped runs after the new public tree has replaced the old one,
	// but before the ordinary post-build callbacks. A process loss after this
	// point leaves a durable "file_written" fact for startup reconciliation.
	OnOutputSwapped func(BuildProgress) error
	// OnBuildComplete runs after the output has been swapped, the normal
	// callbacks have completed, and source/public tree fingerprints have been
	// observed under ContentMu. It is intended for durable operational facts
	// such as the publication manifest; it must not treat those derived facts
	// as a replacement for the filesystem.
	OnBuildComplete func(BuildCompletion) error
}

// BuildProgress is the minimum safe payload for a recovery journal. It
// deliberately contains no filesystem path or caller secret; the build ID and
// time are sufficient to correlate a retained record with server logs.
type BuildProgress struct {
	BuildID    string
	ObservedAt time.Time
}

type BuildPageChange struct {
	SourceKey   string
	Lang        string
	Draft       bool
	TestContent bool
	Deleted     bool
}

// BuildCompletion contains the facts a post-build recorder may persist. The
// hashes are calculated while runBuild holds hugosite.ContentMu, so they
// describe bytes read from disk for this completed build, not an earlier
// in-memory index snapshot.
type BuildCompletion struct {
	BuildID        string
	SourceRevision string
	OutputRevision string
	HugoVersion    string
	Status         string
	ObservedAt     time.Time
}

func notifyBuildFailed(callbacks []PostBuildCallback, progress BuildProgress, state string) {
	progress.ObservedAt = time.Now().UTC()
	for _, cb := range callbacks {
		if cb.OnBuildFailed == nil {
			continue
		}
		if err := cb.OnBuildFailed(progress, state); err != nil {
			slog.Warn("build_site: failed to persist failed build state", "callback", cb.Name, "error", err)
		}
	}
}

func RegisterBuild(s *mcp.Server, cfg config.Config, srcIdx *hugosite.SourceIndex, changeSets *changeset.Registry, siteReload ...PostBuildCallback) {
	if s == nil {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "build_site",
		Title: "Build website",
		Description: "Build the Hugo site and return the build duration in milliseconds. Use this after content changes or before publishing. Returns build_in_progress if another build or content mutation is active. Response is stage-aware (`data.stages`: hugo_build, output_swap, source/public index reload, per-callback outcomes) and page-aware (`data.pages`: which changed translations were included vs excluded_drafts or deleted). With operational SQLite configured, the page set is derived from fresh on-disk source fingerprints compared with the latest completed build and survives restart/external writes; without it, volatile BuildPending remains a compatibility fallback (#858, #1077). " +
			"Optional `change_set_id` (#1140) guards against publishing a different change-set's in-flight edits: if any pending page is known to belong to a change-set the caller hasn't acknowledged, the build is refused with `foreign_change_set_present` rather than silently building it. When two or more change-sets genuinely have pending work at once, pass every one of them in `change_set_ids` instead — a single `change_set_id` can never account for another change-set's real pending page, so without this a concurrent-editing scenario would deadlock (neither side's build could ever succeed). This only catches pending pages this same running process has tracked mutations for since it last started — it is not full external-source-drift detection. " +
			"Optional `expected_source_revision`/`expected_public_revision` (#1141) are optimistic-concurrency guards: obtain a revision from `get_runtime_status` (`include_revisions:true`) and pass it here to have the build refuse with `source_revision_conflict`/`public_revision_conflict` if the tree has changed since you read it, instead of silently building/promoting over a change you never saw. Independently of those (always on, no input required), every build re-verifies the source tree is unchanged immediately before promoting — a mismatch (only possible via a direct filesystem write outside this server, since ContentMu already blocks every other MCP mutation during a build) fails with `source_changed_during_build` and the freshly built output is discarded, never published.",
		InputSchema:  tools.MustSchema[buildSiteInput](),
		OutputSchema: tools.MustSchema[buildSiteOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in buildSiteInput) (*mcp.CallToolResult, buildSiteOutput, error) {
		if err := guardForeignChangeSet(ctx, changeSets, srcIdx, in.ChangeSetID, in.ChangeSetIDs); err != nil {
			return nil, buildSiteOutput{}, err
		}
		data, err := runBuild(ctx, cfg, srcIdx, in.ExpectedSourceRevision, in.ExpectedPublicRevision, siteReload...)
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

func classifyBuildPageChanges(changes []BuildPageChange) buildPagesDTO {
	included, excluded, deleted := []string{}, []string{}, []string{}
	for _, change := range changes {
		key := sourceKeyOf(change.SourceKey, change.Lang)
		switch {
		case change.Deleted:
			deleted = append(deleted, key)
		case change.Draft || change.TestContent:
			excluded = append(excluded, key)
		default:
			included = append(included, key)
		}
	}
	sort.Strings(included)
	sort.Strings(excluded)
	sort.Strings(deleted)
	return buildPagesDTO{Included: included, ExcludedDrafts: excluded, DeletedOutputs: deleted}
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
//
// expectedSourceRevision/expectedPublicRevision (#1141) are optimistic-
// concurrency guards: when non-empty, the caller is asserting "I read the
// source/public tree at this exact revision, reject if it has since
// changed" rather than silently building/promoting over a change they
// never saw. Checked inside the ContentMu lock, immediately after it is
// acquired, so nothing can land between this check and the build actually
// starting. Both are computed via hashTree, the same full-tree hash
// get_runtime_status's own include_revisions option exposes — this
// guard works identically with or without db_path configured, since it
// never touches SQLite.
//
// Independently of expectedSourceRevision, runBuild always re-hashes the
// source tree immediately before promoting the build (source_changed_
// during_build) — ContentMu already serializes every MCP-originated
// mutation against a build in progress (create_page et al. poll for the
// same lock rather than proceeding), so this specifically catches a
// direct filesystem write bypassing this server entirely (e.g. an SSH
// session editing content_root mid-build), the one channel ContentMu
// cannot see. On a mismatch the freshly built output in the temp
// directory is discarded (never passed to swapBuildOutput) and the error
// is returned — the existing `defer os.RemoveAll(buildDir)` cleans it up,
// so nothing unstable is ever promoted.
func runBuild(ctx context.Context, cfg config.Config, srcIdx *hugosite.SourceIndex, expectedSourceRevision, expectedPublicRevision string, siteReload ...PostBuildCallback) (buildSiteData, error) {
	run := buildRun{
		ctx:                    ctx,
		cfg:                    cfg,
		srcIdx:                 srcIdx,
		expectedSourceRevision: expectedSourceRevision,
		expectedPublicRevision: expectedPublicRevision,
		callbacks:              siteReload,
	}
	return run.execute()
}

type buildRun struct {
	ctx                    context.Context
	cfg                    config.Config
	srcIdx                 *hugosite.SourceIndex
	expectedSourceRevision string
	expectedPublicRevision string
	callbacks              []PostBuildCallback
}

type buildPreparation struct {
	sourceRevision string
	buildDir       string
	cacheDir       string
	hugoVersion    string
	timeoutSeconds int
}

func callbackName(callback PostBuildCallback, index int) string {
	if callback.Name != "" {
		return callback.Name
	}
	return fmt.Sprintf("callback %d", index)
}

// prepareCallbacks captures reconciliation changes before recording build
// start. A failure notifies all failure observers exactly once.
func (run *buildRun) prepareCallbacks(progress BuildProgress) ([]BuildPageChange, bool, error) {
	var changes []BuildPageChange
	hasChanges := false
	for index, callback := range run.callbacks {
		if callback.OnBuildPrepared == nil {
			continue
		}
		prepared, err := callback.OnBuildPrepared(progress)
		if err != nil {
			notifyBuildFailed(run.callbacks, progress, "failed:reconciliation")
			return nil, false, fmt.Errorf("build_reconciliation_failed: %s: %w", callbackName(callback, index), err)
		}
		changes = append(changes, prepared...)
		hasChanges = true
	}
	for index, callback := range run.callbacks {
		if callback.OnBuildStart == nil {
			continue
		}
		if err := callback.OnBuildStart(progress); err != nil {
			notifyBuildFailed(run.callbacks, progress, "failed:recovery_record")
			return nil, false, fmt.Errorf("build_recovery_record_failed: %s: %w", callbackName(callback, index), err)
		}
	}
	return changes, hasChanges, nil
}

func (run *buildRun) verifySourceStability(sourceRevision string, progress BuildProgress) error {
	if sourceRevision == "" {
		return nil
	}
	after, err := hashTree(run.cfg.ContentRoot)
	if err != nil {
		buildstatus.RecordFailure("source_fingerprint_failed", time.Now())
		notifyBuildFailed(run.callbacks, progress, "failed:source_fingerprint")
		return fmt.Errorf("internal_error: failed to verify source stability before promotion: %w", err)
	}
	if after != sourceRevision {
		buildstatus.RecordFailure("source_changed_during_build", time.Now())
		notifyBuildFailed(run.callbacks, progress, "failed:source_changed_during_build")
		return fmt.Errorf("source_changed_during_build: source changed while this build was running — this build's output was discarded and never promoted; rebuild once the source is stable")
	}
	return nil
}

type hugoExecution struct {
	args       []string
	stdout     bytes.Buffer
	stderr     bytes.Buffer
	err        error
	durationMs int64
	exitCode   int
}

func (run *buildRun) executeHugo(ctx context.Context, buildDir, cacheDir string, startedAt time.Time) hugoExecution {
	result := hugoExecution{args: append(buildCommandArgs(cacheDir, false), "--destination", buildDir)}
	// #nosec G204 -- executable is fixed to hugo; args come from validated config.
	cmd := exec.CommandContext(ctx, "hugo", result.args...)
	cmd.Dir = run.cfg.HugoRoot
	cmd.Env = boundedCommandEnv()
	setNewProcessGroup(cmd)
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return nil
	}
	cmd.Stderr = &result.stderr
	cmd.Stdout = &result.stdout
	result.err = cmd.Run()
	result.durationMs = time.Since(startedAt).Milliseconds()
	if cmd.ProcessState != nil {
		result.exitCode = cmd.ProcessState.ExitCode()
	}
	return result
}

func (run *buildRun) promote(buildDir string, progress *BuildProgress) (string, error) {
	warning, err := swapBuildOutput(buildDir, run.cfg.SiteRoot)
	if err != nil {
		buildstatus.RecordFailure("output_swap", time.Now())
		notifyBuildFailed(run.callbacks, *progress, "failed:output_swap")
		return "", err
	}
	progress.ObservedAt = time.Now().UTC()
	for index, callback := range run.callbacks {
		if callback.OnOutputSwapped == nil {
			continue
		}
		name := callbackName(callback, index)
		if err := callback.OnOutputSwapped(*progress); err != nil {
			message := fmt.Sprintf("post-output-swap callback %q failed: %v", name, err)
			if warning == "" {
				warning = message
			} else {
				warning += "; " + message
			}
			slog.Warn("build_site: post-output-swap callback failed", "callback", name, "error", err)
		}
	}
	if stillUnreadable := fixUnreadableOutputPaths(run.cfg.SiteRoot); len(stillUnreadable) > 0 {
		message := fmt.Sprintf(
			"output_unreadable: %d path(s) under the published output could not be made servable (likely owned by a different uid) and will 403 for a reverse proxy running as a different user: %s. Fix: sudo chown -R <service-account> %s",
			len(stillUnreadable), strings.Join(stillUnreadable, "; "), run.cfg.SiteRoot,
		)
		if warning == "" {
			warning = message
		} else {
			warning += "; " + message
		}
	}
	return warning, nil
}

func (run *buildRun) runCallbacks(initialWarning string) (map[string]string, string) {
	const callbackTimeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), callbackTimeout)
	defer cancel()
	warning := initialWarning
	outcomes := map[string]string{}

callbackLoop:
	for index, callback := range run.callbacks {
		if callback.Fn == nil {
			continue
		}
		name := callbackName(callback, index)
		done := make(chan error, 1)
		go func(f func() error) { done <- f() }(callback.Fn)
		select {
		case err := <-done:
			if err != nil {
				outcomes[name] = "failed"
				warning = fmt.Sprintf("post-build callback %q failed: %v", name, err)
				slog.Warn("build_site: post-build callback failed", "callback", name, "error", err)
			} else {
				outcomes[name] = "ok"
			}
		case <-ctx.Done():
			outcomes[name] = "timeout"
			warning = fmt.Sprintf("post-build callback %q timed out after %s", name, callbackTimeout)
			slog.Warn("build_site: post-build callback timed out", "callback", name, "timeout", callbackTimeout)
			break callbackLoop
		}
	}
	for _, callback := range run.callbacks {
		if (callback.Fn == nil && callback.OnBuildComplete == nil) || callback.Name == "" {
			continue
		}
		if _, seen := outcomes[callback.Name]; !seen {
			outcomes[callback.Name] = "skipped"
		}
	}
	return outcomes, warning
}

func (run *buildRun) reportCommandFailure(ctx context.Context, execution hugoExecution, cacheDir, buildID string, progress BuildProgress) error {
	cfg := run.cfg
	summary := buildOutputSummary(execution.stderr.Bytes(), execution.stdout.Bytes(), cfg.HugoRoot, cfg.SiteRoot)
	errClass := classifyBuildFailure(ctx, summary)
	slog.Error("build_site failed",
		"build_id", buildID,
		"tool", "build_site",
		"user", currentUserForLog(),
		"command", commandString("hugo", execution.args),
		"cwd", cfg.HugoRoot,
		"cache_dir", cacheDir,
		"duration_ms", execution.durationMs,
		"exit_code", execution.exitCode,
		"error_class", errClass,
		"stdout_tail", outputTail(execution.stdout.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
		"stderr_tail", outputTail(execution.stderr.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
		"output_summary", summary,
		"error", execution.err,
	)
	errKind := "build_error"
	if ctx.Err() != nil {
		errKind = "build_timeout"
	}
	payload := buildErrorPayload{
		Error:            errKind,
		ErrorClass:       errClass,
		ExitCode:         execution.exitCode,
		Command:          commandString("hugo", execution.args),
		WorkingDirectory: cfg.HugoRoot,
		CacheDirectory:   cacheDir,
		DurationMs:       execution.durationMs,
		StderrSummary:    summary,
		StdoutSummary:    outputTail(execution.stdout.Bytes(), cfg.HugoRoot, cfg.SiteRoot),
		BuildID:          buildID,
		LogHint:          "Search server logs for build_id=" + buildID,
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
	notifyBuildFailed(run.callbacks, progress, "failed:"+errClass)
	return fmt.Errorf("build_error: %s", jsonPayload)
}

// prepare validates the deployment and optimistic revisions, then creates the
// isolated output and cache directories. The caller holds ContentMu throughout.
func (run *buildRun) prepare() (buildPreparation, error) {
	cfg := run.cfg
	if run.srcIdx != nil && strings.TrimSpace(cfg.ContentRoot) != "" {
		if err := run.srcIdx.Reload(cfg.ContentRoot); err != nil {
			return buildPreparation{}, fmt.Errorf("source_index_reload_failed: refresh source before build: %w", err)
		}
	}
	if err := checkDirWritable(filepath.Dir(cfg.SiteRoot)); err != nil {
		buildstatus.RecordFailure("permission_denied", time.Now())
		return buildPreparation{}, err
	}
	if err := checkBuildWritable(filepath.Join(cfg.HugoRoot, "resources")); err != nil {
		buildstatus.RecordFailure("permission_denied", time.Now())
		return buildPreparation{}, err
	}

	var sourceRevision string
	if strings.TrimSpace(cfg.ContentRoot) != "" {
		revision, err := hashTree(cfg.ContentRoot)
		if err != nil {
			return buildPreparation{}, fmt.Errorf("internal_error: failed to compute source revision: %w", err)
		}
		sourceRevision = revision
		if run.expectedSourceRevision != "" && revision != run.expectedSourceRevision {
			return buildPreparation{}, fmt.Errorf("source_revision_conflict: source has changed since expected_source_revision was captured (expected %s, actual %s) — re-read the current source revision (get_runtime_status with include_revisions:true) and retry deliberately if the new state is still what you intend to build", run.expectedSourceRevision, revision)
		}
	} else if run.expectedSourceRevision != "" {
		return buildPreparation{}, fmt.Errorf("config_error: expected_source_revision was supplied but content_root is not configured, so it cannot be verified")
	}
	if run.expectedPublicRevision != "" {
		if strings.TrimSpace(cfg.SiteRoot) == "" {
			return buildPreparation{}, fmt.Errorf("config_error: expected_public_revision was supplied but site_root is not configured, so it cannot be verified")
		}
		revision, err := hashTree(cfg.SiteRoot)
		if err != nil {
			return buildPreparation{}, fmt.Errorf("internal_error: failed to compute public revision: %w", err)
		}
		if revision != run.expectedPublicRevision {
			return buildPreparation{}, fmt.Errorf("public_revision_conflict: public output has changed since expected_public_revision was captured (expected %s, actual %s) — someone else may have published in the meantime; re-read the current public revision and retry deliberately if that's intended", run.expectedPublicRevision, revision)
		}
	}

	buildDir, err := os.MkdirTemp(filepath.Dir(cfg.SiteRoot), ".mcp-build-output-")
	if err != nil {
		buildstatus.RecordFailure("permission_denied", time.Now())
		return buildPreparation{}, buildPreflightError(filepath.Dir(cfg.SiteRoot))
	}
	if err := os.Chmod(buildDir, 0o755); err != nil { // #nosec G302 -- public build output must be world-readable
		_ = os.RemoveAll(buildDir)
		buildstatus.RecordFailure("permission_denied", time.Now())
		return buildPreparation{}, buildPreflightError(buildDir)
	}
	timeout := cfg.BuildTimeoutSeconds
	if timeout <= 0 {
		timeout = 120
	}
	cacheDir := hugoCacheDir(cfg)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil { // #nosec G301 -- Hugo cache is a configured service-owned path
		_ = os.RemoveAll(buildDir)
		return buildPreparation{}, fmt.Errorf("config_error: failed to prepare Hugo cache directory")
	}
	return buildPreparation{
		sourceRevision: sourceRevision,
		buildDir:       buildDir,
		cacheDir:       cacheDir,
		hugoVersion:    hugoVersionFromBinary(),
		timeoutSeconds: timeout,
	}, nil
}

func (run *buildRun) execute() (buildSiteData, error) {
	ctx := run.ctx
	cfg := run.cfg
	srcIdx := run.srcIdx
	siteReload := run.callbacks
	if cfg.HugoRoot == "" {
		return buildSiteData{}, fmt.Errorf("config_error: hugo_root is not configured")
	}
	if !hugosite.ContentMu.TryLock() {
		return buildSiteData{}, fmt.Errorf("build_in_progress: a content mutation or build is already running")
	}
	defer hugosite.ContentMu.Unlock()
	prepared, err := run.prepare()
	if err != nil {
		return buildSiteData{}, err
	}
	sourceRevisionBeforeBuild := prepared.sourceRevision
	buildDir := prepared.buildDir
	cacheDir := prepared.cacheDir
	hugoVersion := prepared.hugoVersion
	defer func() { _ = os.RemoveAll(buildDir) }()
	tctx, cancel := context.WithTimeout(ctx, time.Duration(prepared.timeoutSeconds)*time.Second)
	defer cancel()

	start := time.Now()
	runID := newBuildID(start)
	progress := BuildProgress{BuildID: runID, ObservedAt: start.UTC()}
	preparedChanges, hasPreparedChanges, err := run.prepareCallbacks(progress)
	if err != nil {
		return buildSiteData{}, err
	}
	execution := run.executeHugo(tctx, buildDir, cacheDir, start)
	args := execution.args
	durationMs := execution.durationMs
	exitCode := execution.exitCode

	if execution.err != nil {
		return buildSiteData{}, run.reportCommandFailure(tctx, execution, cacheDir, runID, progress)
	}
	buildstatus.RecordSuccess(time.Now())
	// #1141: re-hash the source tree one last time before promoting, and
	// refuse to promote at all if it moved since sourceRevisionBeforeBuild
	// was captured at the top of this function. ContentMu already blocked
	// every MCP-originated mutation for this entire build; a mismatch here
	// can only mean something wrote to content_root outside this server
	// (direct filesystem/SSH access) while the build was running — content
	// that never went through Hugo. Promoting the temp build in that case
	// would silently publish a stale/incomplete render of a source state
	// that no longer exists.
	if err := run.verifySourceStability(sourceRevisionBeforeBuild, progress); err != nil {
		return buildSiteData{}, err
	}
	swapWarning, err := run.promote(buildDir, &progress)
	if err != nil {
		return buildSiteData{}, err
	}

	// Capture the page-aware "changed set" BEFORE the callback loop runs: the
	// index_reload callback calls ClearAllBuildPending(), so pending pages
	// must be snapshotted here or they vanish before we can report them (#858).
	pages := buildPagesDTO{Included: []string{}, ExcludedDrafts: []string{}, DeletedOutputs: []string{}}
	if hasPreparedChanges {
		pages = classifyBuildPageChanges(preparedChanges)
	} else if srcIdx != nil {
		pages = classifyPendingPages(srcIdx.PendingPages())
	}

	callbackOutcomes, cbWarning := run.runCallbacks(swapWarning)
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
	sourceRevision := ""
	if strings.TrimSpace(cfg.ContentRoot) != "" {
		var sourceHashErr error
		sourceRevision, sourceHashErr = hashTree(cfg.ContentRoot)
		if sourceHashErr != nil {
			slog.Warn("build_site: failed to hash source tree", "error", sourceHashErr)
			if cbWarning == "" {
				cbWarning = "source revision unavailable after build"
			} else {
				cbWarning += "; source revision unavailable after build"
			}
			status = "partial_success"
		}
	}
	completion := BuildCompletion{
		BuildID:        runID,
		SourceRevision: sourceRevision,
		OutputRevision: outputRevision,
		HugoVersion:    hugoVersion,
		Status:         status,
		ObservedAt:     time.Now().UTC(),
	}
	for i, cb := range siteReload {
		if cb.OnBuildComplete == nil {
			continue
		}
		name := cb.Name
		if name == "" {
			name = fmt.Sprintf("callback %d", i)
		}
		if err := cb.OnBuildComplete(completion); err != nil {
			callbackOutcomes[name] = "failed"
			cbWarning = fmt.Sprintf("post-build callback %q failed: %v", name, err)
			slog.Warn("build_site: post-build completion callback failed", "callback", name, "error", err)
			status = "partial_success"
			completion.Status = status
		} else {
			callbackOutcomes[name] = "ok"
		}
	}
	stages = buildStages(callbackOutcomes)
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
		SourceRevision: sourceRevisionBeforeBuild,
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
