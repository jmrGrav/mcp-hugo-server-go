package admin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/audit"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/caller"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/mod/semver"
)

const (
	hugoReleaseTimeout      = 20 * time.Second
	hugoChecksumMaxBytes    = 2 * 1024 * 1024
	hugoStageRecordFilename = ".mcp-stage.json"
	hugoActivationFilename  = ".mcp-activation.json"
)

type hugoReleaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type hugoRelease struct {
	TagName     string             `json:"tag_name"`
	PublishedAt time.Time          `json:"published_at"`
	Draft       bool               `json:"draft"`
	Prerelease  bool               `json:"prerelease"`
	Assets      []hugoReleaseAsset `json:"assets"`
}

type hugoLatestInfo struct {
	Version             string `json:"version"`
	PublishedAt         string `json:"published_at,omitempty"`
	UpdateAvailable     bool   `json:"update_available"`
	Compatible          bool   `json:"compatible"`
	CompatibilityReason string `json:"compatibility_reason"`
	Source              string `json:"source"`
	CacheAgeSeconds     int64  `json:"cache_age_seconds,omitempty"`
}

type hugoUpdateStatusData struct {
	Installed        hugoRuntimeStatus `json:"installed"`
	Latest           *hugoLatestInfo   `json:"latest,omitempty"`
	NetworkChecked   bool              `json:"network_checked"`
	ManagedUpgrades  bool              `json:"managed_upgrades_enabled"`
	Platform         string            `json:"platform"`
	Architecture     string            `json:"architecture"`
	ExtendedRequired bool              `json:"extended_required"`
}

type getHugoUpdateStatusInput struct {
	CheckLatest bool `json:"check_latest,omitempty"`
}

type getHugoUpdateStatusOutput struct {
	toolcontract.ToolResponse[hugoUpdateStatusData]
}

type stageHugoUpgradeInput struct {
	TargetVersion string `json:"target_version"`
	DryRun        *bool  `json:"dry_run,omitempty"`
}

type stageHugoUpgradeData struct {
	DryRun                        bool   `json:"dry_run"`
	TargetVersion                 string `json:"target_version"`
	AssetName                     string `json:"asset_name"`
	Checksum                      string `json:"checksum,omitempty"`
	ChecksumVerified              bool   `json:"checksum_verified"`
	VersionVerified               bool   `json:"version_verified"`
	Extended                      bool   `json:"extended"`
	Staged                        bool   `json:"staged"`
	StagedArtifact                string `json:"staged_artifact"`
	ActivationTool                string `json:"activation_tool"`
	ActivationRestartRequired     bool   `json:"activation_restart_required"`
	RollbackTarget                string `json:"rollback_target"`
	RollbackPreservedOnActivation bool   `json:"rollback_preserved_on_activation"`
}

type stageHugoUpgradeOutput struct {
	toolcontract.ToolResponse[stageHugoUpgradeData]
}

type activateHugoUpgradeInput struct {
	TargetVersion string `json:"target_version"`
	DryRun        *bool  `json:"dry_run,omitempty"`
}

type activateHugoUpgradeData struct {
	DryRun          bool   `json:"dry_run"`
	TargetVersion   string `json:"target_version"`
	Activated       bool   `json:"activated"`
	PreviousVersion string `json:"previous_version,omitempty"`
	Checksum        string `json:"checksum"`
	RestartRequired bool   `json:"restart_required"`
	OperatorAction  string `json:"operator_action"`
}

type activateHugoUpgradeOutput struct {
	toolcontract.ToolResponse[activateHugoUpgradeData]
}

type rollbackHugoUpgradeInput struct {
	DryRun *bool `json:"dry_run,omitempty"`
}

type rollbackHugoUpgradeData struct {
	DryRun          bool   `json:"dry_run"`
	RolledBack      bool   `json:"rolled_back"`
	RestoredVersion string `json:"restored_version"`
	Checksum        string `json:"checksum"`
	RestartRequired bool   `json:"restart_required"`
	OperatorAction  string `json:"operator_action"`
}

type rollbackHugoUpgradeOutput struct {
	toolcontract.ToolResponse[rollbackHugoUpgradeData]
}

type bootstrapHugoInput struct {
	DryRun *bool `json:"dry_run,omitempty"`
}

type bootstrapHugoData struct {
	DryRun          bool   `json:"dry_run"`
	DetectedVersion string `json:"detected_version"`
	Staged          bool   `json:"staged"`
	Activated       bool   `json:"activated"`
	Checksum        string `json:"checksum,omitempty"`
	RestartRequired bool   `json:"restart_required"`
	OperatorAction  string `json:"operator_action,omitempty"`
}

type bootstrapHugoOutput struct {
	toolcontract.ToolResponse[bootstrapHugoData]
}

type hugoStageRecord struct {
	Version         string `json:"version"`
	ArchiveChecksum string `json:"archive_checksum"`
	BinaryChecksum  string `json:"binary_checksum"`
	AssetName       string `json:"asset_name"`
	Extended        bool   `json:"extended"`
	BinaryRel       string `json:"binary_rel"`
	StagedAt        string `json:"staged_at"`
}

type hugoActivationRecord struct {
	ActiveVersion    string `json:"active_version"`
	ActiveTarget     string `json:"active_target"`
	PreviousVersion  string `json:"previous_version,omitempty"`
	PreviousTarget   string `json:"previous_target,omitempty"`
	PreviousChecksum string `json:"previous_checksum,omitempty"`
	PreviousExtended bool   `json:"previous_extended,omitempty"`
	ActivatedAt      string `json:"activated_at"`
}

type hugoUpgradeManager struct {
	cfg       config.Config
	client    *http.Client
	mu        sync.Mutex
	latest    *hugoRelease
	fetchedAt time.Time
}

var installedHugoSemverPattern = regexp.MustCompile(`(?:^|v)(\d+\.\d+\.\d+)`)

func newHugoUpgradeManager(cfg config.Config) *hugoUpgradeManager {
	m := &hugoUpgradeManager{cfg: cfg}
	m.client = &http.Client{
		Timeout: hugoReleaseTimeout,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if !m.allowedURL(req.URL) {
				return fmt.Errorf("redirect host is not allowlisted")
			}
			return nil
		},
	}
	return m
}

func RegisterHugoUpgradeTools(s *mcp.Server, cfg config.Config) {
	if s == nil {
		return
	}
	mgr := newHugoUpgradeManager(cfg)
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_hugo_update", Title: "Get Hugo update status",
		Description: "Report the installed Hugo version and optionally compare it with the latest stable official Hugo release. No network request is made unless check_latest=true; a bounded cached result may be returned otherwise. This read-only status call never downloads, installs, activates, or restarts anything. Requires write on this operator deployment.",
		InputSchema: tools.MustSchema[getHugoUpdateStatusInput](), OutputSchema: tools.MustSchema[getHugoUpdateStatusOutput](),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: fileutil.BoolPtr(false), IdempotentHint: true, OpenWorldHint: fileutil.BoolPtr(true)},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in getHugoUpdateStatusInput) (*mcp.CallToolResult, getHugoUpdateStatusOutput, error) {
		data, err := mgr.status(ctx, in.CheckLatest)
		if err != nil {
			return nil, getHugoUpdateStatusOutput{}, err
		}
		return nil, getHugoUpdateStatusOutput{ToolResponse: hugoUpgradeSuccess(data)}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "stage_hugo_upgrade", Title: "Stage Hugo upgrade",
		Description: "Resolve and verify an exact official Hugo release without replacing the active binary. dry_run defaults to true. A real stage is disabled unless hugo_upgrade.enabled=true, downloads only allowlisted release assets with a bounded size, verifies the official SHA-256 manifest, extracts only the Hugo executable into the managed directory, and runs only the staged binary's version command. Requires write.",
		InputSchema: tools.MustSchema[stageHugoUpgradeInput](), OutputSchema: tools.MustSchema[stageHugoUpgradeOutput](),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(false), IdempotentHint: false, OpenWorldHint: fileutil.BoolPtr(true)},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in stageHugoUpgradeInput) (*mcp.CallToolResult, stageHugoUpgradeOutput, error) {
		data, err := mgr.stage(ctx, in)
		if err != nil {
			mgr.audit(ctx, "stage_hugo_upgrade", "failed", normalizeVersion(in.TargetVersion), "", err)
			return nil, stageHugoUpgradeOutput{}, err
		}
		mgr.audit(ctx, "stage_hugo_upgrade", "success", data.TargetVersion, data.Checksum, nil)
		return nil, stageHugoUpgradeOutput{ToolResponse: hugoUpgradeSuccess(data)}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "activate_hugo", Title: "Activate Hugo upgrade",
		Description: "Atomically point the configured managed Hugo symlink at an already staged and re-verified exact version. dry_run defaults to true. Refuses package-manager or arbitrary paths, preserves the previous managed target for rollback, writes an audit event, and never restarts the service implicitly. Requires write.",
		InputSchema: tools.MustSchema[activateHugoUpgradeInput](), OutputSchema: tools.MustSchema[activateHugoUpgradeOutput](),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(false), IdempotentHint: false, OpenWorldHint: fileutil.BoolPtr(false)},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in activateHugoUpgradeInput) (*mcp.CallToolResult, activateHugoUpgradeOutput, error) {
		data, err := mgr.activate(ctx, in)
		if err != nil {
			mgr.audit(ctx, "activate_hugo", "failed", normalizeVersion(in.TargetVersion), "", err)
			return nil, activateHugoUpgradeOutput{}, err
		}
		mgr.audit(ctx, "activate_hugo", "success", data.TargetVersion, data.Checksum, nil)
		return nil, activateHugoUpgradeOutput{ToolResponse: hugoUpgradeSuccess(data)}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "rollback_hugo", Title: "Rollback Hugo upgrade",
		Description: "Atomically restore the exact previous managed Hugo symlink target recorded by activate_hugo. dry_run defaults to true. Refuses unmanaged or changed targets and never restarts the service implicitly. Requires write.",
		InputSchema: tools.MustSchema[rollbackHugoUpgradeInput](), OutputSchema: tools.MustSchema[rollbackHugoUpgradeOutput](),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(false), IdempotentHint: false, OpenWorldHint: fileutil.BoolPtr(false)},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in rollbackHugoUpgradeInput) (*mcp.CallToolResult, rollbackHugoUpgradeOutput, error) {
		data, err := mgr.rollback(ctx, in)
		if err != nil {
			mgr.audit(ctx, "rollback_hugo", "failed", "", "", err)
			return nil, rollbackHugoUpgradeOutput{}, err
		}
		mgr.audit(ctx, "rollback_hugo", "success", data.RestoredVersion, data.Checksum, nil)
		return nil, rollbackHugoUpgradeOutput{ToolResponse: hugoUpgradeSuccess(data)}, nil
	}))

	mcp.AddTool(s, &mcp.Tool{
		Name: "bootstrap_hugo", Title: "Bootstrap managed Hugo",
		Description: "One-time setup for a deployment that has never used managed Hugo upgrades: detects the currently installed Hugo version, then re-downloads and checksum-verifies that exact version from the official release (never trusts the existing binary on disk directly), stages it, and activates it as the initial managed baseline. dry_run defaults to true. Refuses to run if a managed version is already active — use stage_hugo_upgrade/activate_hugo for every upgrade after this. Without this step, rollback_hugo has no target to restore on a system's very first activation, because the pre-existing unmanaged binary was never itself a managed version. Requires write.",
		InputSchema: tools.MustSchema[bootstrapHugoInput](), OutputSchema: tools.MustSchema[bootstrapHugoOutput](),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: fileutil.BoolPtr(false), IdempotentHint: false, OpenWorldHint: fileutil.BoolPtr(true)},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in bootstrapHugoInput) (*mcp.CallToolResult, bootstrapHugoOutput, error) {
		data, err := mgr.bootstrap(ctx, in)
		if err != nil {
			mgr.audit(ctx, "bootstrap_hugo", "failed", data.DetectedVersion, "", err)
			return nil, bootstrapHugoOutput{}, err
		}
		mgr.audit(ctx, "bootstrap_hugo", "success", data.DetectedVersion, data.Checksum, nil)
		return nil, bootstrapHugoOutput{ToolResponse: hugoUpgradeSuccess(data)}, nil
	}))
}

func hugoUpgradeSuccess[T any](data T) toolcontract.ToolResponse[T] {
	return toolcontract.Success(data, toolcontract.NewMeta(buildinfo.Version, time.Now().UTC()))
}

func dryRunDefaultTrue(v *bool) bool {
	return v == nil || *v
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) || semver.Prerelease(v) != "" || semver.Build(v) != "" {
		return ""
	}
	return v
}

func comparableInstalledVersion(v string) string {
	m := installedHugoSemverPattern.FindStringSubmatch(strings.TrimSpace(v))
	if len(m) != 2 {
		return ""
	}
	return "v" + m[1]
}

func (m *hugoUpgradeManager) status(ctx context.Context, checkLatest bool) (hugoUpdateStatusData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	installed := probeHugo(ctx, m.cfg)
	data := hugoUpdateStatusData{
		Installed: installed, ManagedUpgrades: m.cfg.HugoUpgrade.Enabled,
		Platform: runtime.GOOS, Architecture: runtime.GOARCH, ExtendedRequired: m.cfg.HugoUpgrade.RequireExtended,
	}
	now := time.Now()
	if checkLatest {
		rel, source, err := m.latestRelease(ctx, now)
		if err != nil {
			return data, err
		}
		data.NetworkChecked = source == "live"
		data.Latest = m.latestInfo(rel, installed.Version, source, now.Sub(m.fetchedAt))
		return data, nil
	}
	if m.latest != nil {
		data.Latest = m.latestInfo(*m.latest, installed.Version, "cache", now.Sub(m.fetchedAt))
	}
	return data, nil
}

func (m *hugoUpgradeManager) latestInfo(rel hugoRelease, installed, source string, age time.Duration) *hugoLatestInfo {
	installedV := comparableInstalledVersion(installed)
	compatible := runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64")
	reason := "official managed archive is available for this platform and variant"
	if compatible {
		_, compatible = exactReleaseAsset(rel.Assets, hugoArchiveName(rel.TagName, m.cfg.HugoUpgrade.RequireExtended, runtime.GOOS, runtime.GOARCH))
	}
	if !compatible {
		reason = "no exact supported official managed archive is available for this platform and variant"
	}
	return &hugoLatestInfo{
		Version: strings.TrimPrefix(rel.TagName, "v"), PublishedAt: rel.PublishedAt.UTC().Format(time.RFC3339),
		UpdateAvailable: installedV != "" && semver.Compare(rel.TagName, installedV) > 0,
		Compatible:      compatible, CompatibilityReason: reason,
		Source: source, CacheAgeSeconds: max(0, int64(age.Seconds())),
	}
}

func (m *hugoUpgradeManager) latestRelease(ctx context.Context, now time.Time) (hugoRelease, string, error) {
	ttl := time.Duration(m.cfg.HugoUpgrade.CacheTTLSeconds) * time.Second
	if m.latest != nil && ttl > 0 && now.Sub(m.fetchedAt) < ttl {
		return *m.latest, "cache", nil
	}
	var rel hugoRelease
	if err := m.fetchJSON(ctx, strings.TrimRight(m.cfg.HugoUpgrade.ReleaseAPIBaseURL, "/")+"/releases/latest", &rel); err != nil {
		return hugoRelease{}, "", fmt.Errorf("release_check_failed: latest official Hugo release could not be retrieved: %w", err)
	}
	if err := validateStableRelease(rel); err != nil {
		return hugoRelease{}, "", err
	}
	m.latest = &rel
	m.fetchedAt = now
	return rel, "live", nil
}

func (m *hugoUpgradeManager) releaseByVersion(ctx context.Context, version string) (hugoRelease, error) {
	var rel hugoRelease
	endpoint := strings.TrimRight(m.cfg.HugoUpgrade.ReleaseAPIBaseURL, "/") + "/releases/tags/" + url.PathEscape(version)
	if err := m.fetchJSON(ctx, endpoint, &rel); err != nil {
		return rel, fmt.Errorf("release_check_failed: target Hugo release could not be retrieved: %w", err)
	}
	if err := validateStableRelease(rel); err != nil {
		return rel, err
	}
	if rel.TagName != version {
		return rel, fmt.Errorf("version_mismatch: release API returned %q for requested %q", rel.TagName, version)
	}
	return rel, nil
}

func validateStableRelease(rel hugoRelease) error {
	if normalizeVersion(rel.TagName) == "" || rel.TagName != normalizeVersion(rel.TagName) || rel.Draft || rel.Prerelease {
		return fmt.Errorf("release_invalid: release metadata is not an exact stable semantic version")
	}
	return nil
}

func (m *hugoUpgradeManager) stage(ctx context.Context, in stageHugoUpgradeInput) (stageHugoUpgradeData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stageLocked(ctx, in)
}

// stageLocked is stage's body, factored out so bootstrap can call it and
// activateLocked in the same critical section without deadlocking on m.mu
// (sync.Mutex is not reentrant). Callers must already hold m.mu.
func (m *hugoUpgradeManager) stageLocked(ctx context.Context, in stageHugoUpgradeInput) (stageHugoUpgradeData, error) {
	if !m.cfg.HugoUpgrade.Enabled {
		return stageHugoUpgradeData{}, fmt.Errorf("config_error: managed Hugo upgrades are disabled")
	}
	version := normalizeVersion(in.TargetVersion)
	if version == "" {
		return stageHugoUpgradeData{}, fmt.Errorf("invalid_params: target_version must be an exact stable semantic version")
	}
	if minimum := normalizeVersion(m.cfg.HugoUpgrade.MinimumVersion); minimum != "" && semver.Compare(version, minimum) < 0 {
		return stageHugoUpgradeData{}, fmt.Errorf("version_policy_denied: target_version is below the configured minimum")
	}
	if maximum := normalizeVersion(m.cfg.HugoUpgrade.MaximumVersion); maximum != "" && semver.Compare(version, maximum) > 0 {
		return stageHugoUpgradeData{}, fmt.Errorf("version_policy_denied: target_version is above the configured maximum")
	}
	if runtime.GOOS != "linux" || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return stageHugoUpgradeData{}, fmt.Errorf("config_error: managed Hugo staging currently supports linux amd64/arm64 only")
	}
	installed := probeHugo(ctx, m.cfg)
	if installed.Available && !m.cfg.HugoUpgrade.AllowDowngrade && semver.Compare(version, comparableInstalledVersion(installed.Version)) < 0 {
		return stageHugoUpgradeData{}, fmt.Errorf("downgrade_forbidden: target_version is older than the installed Hugo version")
	}
	rel, err := m.releaseByVersion(ctx, version)
	if err != nil {
		return stageHugoUpgradeData{}, err
	}
	assetName := hugoArchiveName(version, m.cfg.HugoUpgrade.RequireExtended, runtime.GOOS, runtime.GOARCH)
	checksumName := "hugo_" + strings.TrimPrefix(version, "v") + "_checksums.txt"
	archiveAsset, ok := exactReleaseAsset(rel.Assets, assetName)
	if !ok {
		return stageHugoUpgradeData{}, fmt.Errorf("release_invalid: release has no exact asset %q", assetName)
	}
	checksumAsset, ok := exactReleaseAsset(rel.Assets, checksumName)
	if !ok {
		return stageHugoUpgradeData{}, fmt.Errorf("release_invalid: release has no exact checksum manifest %q", checksumName)
	}
	data := stageHugoUpgradeData{
		DryRun: dryRunDefaultTrue(in.DryRun), TargetVersion: version, AssetName: assetName,
		Extended: m.cfg.HugoUpgrade.RequireExtended, StagedArtifact: "versions/" + version + "/hugo", ActivationTool: "activate_hugo",
		ActivationRestartRequired: true, RollbackTarget: "current_managed_target_at_activation", RollbackPreservedOnActivation: true,
	}
	if data.DryRun {
		return data, nil
	}
	manifest, err := m.download(ctx, checksumAsset.URL, hugoChecksumMaxBytes)
	if err != nil {
		return data, fmt.Errorf("download_error: checksum manifest: %w", err)
	}
	wantChecksum, err := checksumForAsset(manifest, assetName)
	if err != nil {
		return data, err
	}
	archive, err := m.download(ctx, archiveAsset.URL, m.cfg.HugoUpgrade.MaxDownloadBytes)
	if err != nil {
		return data, fmt.Errorf("download_error: Hugo archive: %w", err)
	}
	gotSum := sha256.Sum256(archive)
	gotChecksum := hex.EncodeToString(gotSum[:])
	if gotChecksum != wantChecksum {
		return data, fmt.Errorf("checksum_mismatch: downloaded Hugo archive does not match the official manifest")
	}
	if err := m.ensureManagedRoot(); err != nil {
		return data, err
	}
	versionsDir := filepath.Join(m.cfg.HugoUpgrade.ManagedDir, "versions")
	if err := os.MkdirAll(versionsDir, 0o750); err != nil {
		return data, fmt.Errorf("permission_denied: managed versions directory could not be created")
	}
	if err := requireRealPath(versionsDir); err != nil {
		return data, err
	}
	finalDir := filepath.Join(versionsDir, version)
	if _, err := os.Lstat(finalDir); err == nil {
		return data, fmt.Errorf("already_exists: target Hugo version is already staged")
	} else if !os.IsNotExist(err) {
		return data, fmt.Errorf("read_error: staged target could not be inspected")
	}
	tmpDir, err := os.MkdirTemp(versionsDir, ".stage-*")
	if err != nil {
		return data, fmt.Errorf("permission_denied: staging directory could not be created")
	}
	defer os.RemoveAll(tmpDir)
	binaryPath := filepath.Join(tmpDir, "hugo")
	if err := extractHugoTarGz(archive, binaryPath, m.cfg.HugoUpgrade.MaxDownloadBytes); err != nil {
		return data, err
	}
	if err := verifyStagedHugo(ctx, binaryPath, version, m.cfg.HugoUpgrade.RequireExtended); err != nil {
		return data, err
	}
	binaryChecksum, err := fileSHA256(binaryPath)
	if err != nil {
		return data, err
	}
	record := hugoStageRecord{
		Version: version, ArchiveChecksum: gotChecksum, BinaryChecksum: binaryChecksum, AssetName: assetName, Extended: m.cfg.HugoUpgrade.RequireExtended,
		BinaryRel: filepath.ToSlash(filepath.Join("versions", version, "hugo")), StagedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.MarshalIndent(record, "", "  ")
	if err := os.WriteFile(filepath.Join(tmpDir, hugoStageRecordFilename), append(raw, '\n'), 0o600); err != nil {
		return data, fmt.Errorf("permission_denied: staged verification record could not be written")
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return data, fmt.Errorf("activation_error: staged Hugo directory could not be promoted atomically")
	}
	data.Checksum = gotChecksum
	data.ChecksumVerified = true
	data.VersionVerified = true
	data.Staged = true
	return data, nil
}

func (m *hugoUpgradeManager) activate(ctx context.Context, in activateHugoUpgradeInput) (activateHugoUpgradeData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activateLocked(ctx, in)
}

// activateLocked is activate's body, factored out so bootstrap can call it
// after stageLocked in the same critical section. Callers must already hold
// m.mu.
func (m *hugoUpgradeManager) activateLocked(ctx context.Context, in activateHugoUpgradeInput) (activateHugoUpgradeData, error) {
	if !m.cfg.HugoUpgrade.Enabled {
		return activateHugoUpgradeData{}, fmt.Errorf("config_error: managed Hugo upgrades are disabled")
	}
	version := normalizeVersion(in.TargetVersion)
	if version == "" {
		return activateHugoUpgradeData{}, fmt.Errorf("invalid_params: target_version must be an exact stable semantic version")
	}
	record, binaryPath, err := m.loadStageRecord(version)
	if err != nil {
		return activateHugoUpgradeData{}, err
	}
	if err := verifyFileChecksum(binaryPath, record.BinaryChecksum); err != nil {
		return activateHugoUpgradeData{}, err
	}
	if err := verifyStagedHugo(ctx, binaryPath, version, record.Extended); err != nil {
		return activateHugoUpgradeData{}, err
	}
	data := activateHugoUpgradeData{
		DryRun: dryRunDefaultTrue(in.DryRun), TargetVersion: version, Checksum: record.BinaryChecksum, RestartRequired: true,
		OperatorAction: "restart the MCP service through the configured supervisor, then call get_hugo_update",
	}
	oldTarget, oldVersion, err := m.currentManagedTarget()
	if err != nil {
		return data, err
	}
	var oldChecksum string
	var oldExtended bool
	if oldTarget != "" {
		if oldVersion == "" {
			return data, fmt.Errorf("unmanaged_path: current Hugo symlink is not a versioned managed target")
		}
		oldPath, resolveErr := m.resolveManagedRelative(oldTarget)
		if resolveErr != nil {
			return data, resolveErr
		}
		oldChecksum, err = fileSHA256(oldPath)
		if err != nil {
			return data, err
		}
		oldExtended, err = inspectStagedHugo(ctx, oldPath, oldVersion)
		if err != nil {
			return data, err
		}
	}
	data.PreviousVersion = oldVersion
	if data.DryRun {
		return data, nil
	}
	newTarget := record.BinaryRel
	if err := m.swapManagedLink(newTarget); err != nil {
		return data, err
	}
	activation := hugoActivationRecord{
		ActiveVersion: version, ActiveTarget: newTarget, PreviousVersion: oldVersion,
		PreviousTarget: oldTarget, PreviousChecksum: oldChecksum, PreviousExtended: oldExtended,
		ActivatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, _ := json.MarshalIndent(activation, "", "  ")
	if err := fileutil.AtomicWrite(filepath.Join(m.cfg.HugoUpgrade.ManagedDir, hugoActivationFilename), string(append(raw, '\n'))); err != nil {
		_ = m.restoreManagedLink(oldTarget)
		return data, fmt.Errorf("activation_error: activation record could not be persisted; symlink was restored")
	}
	data.Activated = true
	return data, nil
}

// bootstrap gives a fresh deployment (no managed Hugo version has ever been
// activated) a legitimate rollback target from the very first real upgrade.
// Without this, rollback_hugo has nothing to restore on that first upgrade's
// activation: currentManagedTarget() reports no prior managed target (the
// pre-existing unmanaged binary was never staged/activated through this
// feature), so activate's own activation record has an empty
// previous_target — by design, since it has never lied about a rollback
// target existing when it didn't.
//
// This does not adopt or trust the binary already on disk. It re-downloads
// and checksum-verifies the detected version fresh from the official
// release, through the exact same stageLocked path stage_hugo_upgrade uses,
// so every managed binary's provenance stays uniformly verified — the whole
// point of this feature. The redundant download (you already have this
// exact version installed) is the accepted cost of that guarantee.
func (m *hugoUpgradeManager) bootstrap(ctx context.Context, in bootstrapHugoInput) (bootstrapHugoData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.HugoUpgrade.Enabled {
		return bootstrapHugoData{}, fmt.Errorf("config_error: managed Hugo upgrades are disabled")
	}
	existingTarget, _, err := m.currentManagedTarget()
	if err != nil {
		return bootstrapHugoData{}, err
	}
	if existingTarget != "" {
		return bootstrapHugoData{}, fmt.Errorf("bootstrap_unavailable: a managed Hugo version is already active; use stage_hugo_upgrade/activate_hugo instead")
	}
	installed := probeHugo(ctx, m.cfg)
	version := comparableInstalledVersion(installed.Version)
	if !installed.Available || version == "" {
		return bootstrapHugoData{}, fmt.Errorf("config_error: no installed Hugo binary with a recognizable version was detected to bootstrap from")
	}
	data := bootstrapHugoData{DryRun: dryRunDefaultTrue(in.DryRun), DetectedVersion: version, RestartRequired: true}
	if data.DryRun {
		return data, nil
	}
	notDryRun := false
	stageData, err := m.stageLocked(ctx, stageHugoUpgradeInput{TargetVersion: version, DryRun: &notDryRun})
	if err != nil {
		return data, err
	}
	data.Staged = stageData.Staged
	activateData, err := m.activateLocked(ctx, activateHugoUpgradeInput{TargetVersion: version, DryRun: &notDryRun})
	if err != nil {
		return data, err
	}
	data.Activated = activateData.Activated
	data.Checksum = activateData.Checksum
	data.OperatorAction = activateData.OperatorAction
	return data, nil
}

func (m *hugoUpgradeManager) rollback(ctx context.Context, in rollbackHugoUpgradeInput) (rollbackHugoUpgradeData, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.cfg.HugoUpgrade.Enabled {
		return rollbackHugoUpgradeData{}, fmt.Errorf("config_error: managed Hugo upgrades are disabled")
	}
	activation, err := m.loadActivationRecord()
	if err != nil {
		return rollbackHugoUpgradeData{}, err
	}
	if activation.PreviousTarget == "" {
		return rollbackHugoUpgradeData{}, fmt.Errorf("rollback_unavailable: activation has no previous managed target")
	}
	current, _, err := m.currentManagedTarget()
	if err != nil {
		return rollbackHugoUpgradeData{}, err
	}
	if current != activation.ActiveTarget {
		return rollbackHugoUpgradeData{}, fmt.Errorf("revision_conflict: managed Hugo symlink changed after activation")
	}
	previousPath, err := m.resolveManagedRelative(activation.PreviousTarget)
	if err != nil {
		return rollbackHugoUpgradeData{}, err
	}
	if err := requireRegularFile(previousPath); err != nil {
		return rollbackHugoUpgradeData{}, fmt.Errorf("rollback_unavailable: previous managed Hugo binary is missing")
	}
	if err := verifyFileChecksum(previousPath, activation.PreviousChecksum); err != nil {
		return rollbackHugoUpgradeData{}, err
	}
	if err := verifyStagedHugo(ctx, previousPath, activation.PreviousVersion, activation.PreviousExtended); err != nil {
		return rollbackHugoUpgradeData{}, err
	}
	data := rollbackHugoUpgradeData{
		DryRun: dryRunDefaultTrue(in.DryRun), RestoredVersion: activation.PreviousVersion,
		Checksum: activation.PreviousChecksum, RestartRequired: true,
		OperatorAction: "restart the MCP service through the configured supervisor, then call get_hugo_update",
	}
	if data.DryRun {
		return data, nil
	}
	if err := m.swapManagedLink(activation.PreviousTarget); err != nil {
		return data, err
	}
	if err := os.Remove(filepath.Join(m.cfg.HugoUpgrade.ManagedDir, hugoActivationFilename)); err != nil && !os.IsNotExist(err) {
		_ = m.swapManagedLink(activation.ActiveTarget)
		return data, fmt.Errorf("rollback_error: activation record could not be removed; active target was restored")
	}
	data.RolledBack = true
	return data, nil
}

func (m *hugoUpgradeManager) fetchJSON(ctx context.Context, endpoint string, out any) error {
	body, err := m.download(ctx, endpoint, hugoChecksumMaxBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("response was not valid JSON")
	}
	return nil
}

func (m *hugoUpgradeManager) download(ctx context.Context, endpoint string, limit int64) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil || !m.allowedURL(u) {
		return nil, fmt.Errorf("URL host is not allowlisted")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("request could not be created")
	}
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	req.Header.Set("User-Agent", "mcp-hugo-server-go/"+buildinfo.Version)
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("response exceeds configured size limit")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("response could not be read")
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds configured size limit")
	}
	return body, nil
}

func (m *hugoUpgradeManager) allowedURL(u *url.URL) bool {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Hostname() == "" {
		return false
	}
	base, err := url.Parse(m.cfg.HugoUpgrade.ReleaseAPIBaseURL)
	if err != nil || base.Scheme == "" || u.Scheme != base.Scheme {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, allowed := range m.cfg.HugoUpgrade.AllowedHosts {
		if host == strings.ToLower(strings.TrimSuffix(strings.TrimSpace(allowed), ".")) {
			return true
		}
	}
	return false
}

func hugoArchiveName(version string, extended bool, goos, goarch string) string {
	prefix := "hugo_"
	if extended {
		prefix = "hugo_extended_"
	}
	return prefix + strings.TrimPrefix(version, "v") + "_" + goos + "-" + goarch + ".tar.gz"
}

func exactReleaseAsset(assets []hugoReleaseAsset, name string) (hugoReleaseAsset, bool) {
	var found hugoReleaseAsset
	count := 0
	for _, asset := range assets {
		if asset.Name == name {
			found = asset
			count++
		}
	}
	return found, count == 1 && found.URL != ""
}

func checksumForAsset(manifest []byte, assetName string) (string, error) {
	var found string
	for _, line := range strings.Split(string(manifest), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != assetName {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return "", fmt.Errorf("release_invalid: checksum manifest has an invalid SHA-256 for the selected asset")
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return "", fmt.Errorf("release_invalid: checksum manifest has an invalid SHA-256 for the selected asset")
		}
		if found != "" {
			return "", fmt.Errorf("release_invalid: checksum manifest contains duplicate entries for the selected asset")
		}
		found = strings.ToLower(fields[0])
	}
	if found == "" {
		return "", fmt.Errorf("release_invalid: checksum manifest does not cover the selected asset")
	}
	return found, nil
}

func extractHugoTarGz(archive []byte, destination string, maxBinaryBytes int64) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("archive_invalid: Hugo archive is not valid gzip")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("archive_invalid: Hugo archive could not be read")
		}
		clean := filepath.Clean(filepath.FromSlash(hdr.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive_invalid: Hugo archive contains an unsafe path")
		}
		if filepath.Base(clean) != "hugo" {
			continue
		}
		if found || hdr.Typeflag != tar.TypeReg || hdr.Size <= 0 || hdr.Size > maxBinaryBytes {
			return fmt.Errorf("archive_invalid: Hugo archive contains an invalid executable entry")
		}
		f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o750)
		if err != nil {
			return fmt.Errorf("permission_denied: staged Hugo executable could not be created")
		}
		_, copyErr := io.CopyN(f, tr, hdr.Size)
		closeErr := f.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("archive_invalid: staged Hugo executable was incomplete")
		}
		found = true
	}
	if !found {
		return fmt.Errorf("archive_invalid: Hugo archive contains no executable")
	}
	return nil
}

func verifyStagedHugo(ctx context.Context, binaryPath, version string, requireExtended bool) error {
	extended, err := inspectStagedHugo(ctx, binaryPath, version)
	if err != nil {
		return err
	}
	if requireExtended && !extended {
		return fmt.Errorf("version_mismatch: staged Hugo executable does not report the requested version and variant")
	}
	return nil
}

func inspectStagedHugo(ctx context.Context, binaryPath, version string) (bool, error) {
	tctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(tctx, binaryPath, "version") // #nosec G204 -- path is constrained to the private managed directory.
	cmd.Env = boundedCommandEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("version_mismatch: staged Hugo executable did not run successfully")
	}
	text := string(out)
	if !strings.Contains(text, "hugo "+version) {
		return false, fmt.Errorf("version_mismatch: staged Hugo executable does not report the requested version and variant")
	}
	return strings.Contains(text, "+extended"), nil
}

func verifyFileChecksum(path, want string) error {
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum_mismatch: staged Hugo executable changed after verification")
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("staged_artifact_missing: staged Hugo executable could not be opened")
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("staged_artifact_invalid: staged Hugo executable could not be hashed")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (m *hugoUpgradeManager) ensureManagedRoot() error {
	root := filepath.Clean(m.cfg.HugoUpgrade.ManagedDir)
	if !filepath.IsAbs(root) || root == string(filepath.Separator) {
		return fmt.Errorf("unmanaged_path: managed Hugo directory is invalid")
	}
	link := filepath.Clean(m.cfg.HugoUpgrade.BinaryLink)
	rel, err := filepath.Rel(root, link)
	if !filepath.IsAbs(link) || err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("unmanaged_path: managed Hugo binary link must be inside managed_dir")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("permission_denied: managed Hugo directory could not be created")
	}
	return requireRealPath(root)
}

func requireRealPath(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(path) {
		return fmt.Errorf("unmanaged_path: managed Hugo path contains a symlink")
	}
	return nil
}

func requireRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("unmanaged_path: managed Hugo executable must be a regular file, not a symlink")
	}
	return nil
}

func (m *hugoUpgradeManager) loadStageRecord(version string) (hugoStageRecord, string, error) {
	if err := m.ensureManagedRoot(); err != nil {
		return hugoStageRecord{}, "", err
	}
	dir := filepath.Join(m.cfg.HugoUpgrade.ManagedDir, "versions", version)
	if err := requireRealPath(dir); err != nil {
		return hugoStageRecord{}, "", fmt.Errorf("staged_artifact_missing: target version is not staged")
	}
	raw, err := os.ReadFile(filepath.Join(dir, hugoStageRecordFilename))
	if err != nil {
		return hugoStageRecord{}, "", fmt.Errorf("staged_artifact_missing: verification record is missing")
	}
	var record hugoStageRecord
	if json.Unmarshal(raw, &record) != nil || record.Version != version || record.BinaryChecksum == "" || record.BinaryRel != filepath.ToSlash(filepath.Join("versions", version, "hugo")) {
		return hugoStageRecord{}, "", fmt.Errorf("staged_artifact_invalid: verification record is inconsistent")
	}
	binaryPath, err := m.resolveManagedRelative(record.BinaryRel)
	if err == nil {
		err = requireRegularFile(binaryPath)
	}
	return record, binaryPath, err
}

func (m *hugoUpgradeManager) resolveManagedRelative(rel string) (string, error) {
	if filepath.IsAbs(rel) || rel == "" {
		return "", fmt.Errorf("unmanaged_path: managed target must be relative")
	}
	root := filepath.Clean(m.cfg.HugoUpgrade.ManagedDir)
	full := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	check, err := filepath.Rel(root, full)
	if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unmanaged_path: target escapes managed Hugo directory")
	}
	return full, nil
}

func (m *hugoUpgradeManager) currentManagedTarget() (string, string, error) {
	if err := m.ensureManagedRoot(); err != nil {
		return "", "", err
	}
	link := filepath.Clean(m.cfg.HugoUpgrade.BinaryLink)
	info, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return "", "", nil
	}
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", "", fmt.Errorf("unmanaged_path: configured Hugo binary link is not a managed symlink")
	}
	raw, err := os.Readlink(link)
	if err != nil {
		return "", "", fmt.Errorf("read_error: managed Hugo symlink could not be read")
	}
	resolved := raw
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(link), resolved)
	}
	root := filepath.Clean(m.cfg.HugoUpgrade.ManagedDir)
	resolved, err = filepath.EvalSymlinks(filepath.Clean(resolved))
	if err != nil {
		return "", "", fmt.Errorf("unmanaged_path: current Hugo symlink target is missing or unresolved")
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("unmanaged_path: current Hugo symlink points outside managed_dir")
	}
	rel = filepath.ToSlash(rel)
	version := ""
	parts := strings.Split(rel, "/")
	if len(parts) == 3 && parts[0] == "versions" && parts[2] == "hugo" {
		version = normalizeVersion(parts[1])
	}
	return rel, version, nil
}

func (m *hugoUpgradeManager) swapManagedLink(targetRel string) error {
	target, err := m.resolveManagedRelative(targetRel)
	if err != nil {
		return err
	}
	if err := requireRegularFile(target); err != nil {
		return fmt.Errorf("staged_artifact_missing: managed Hugo target does not exist")
	}
	link := filepath.Clean(m.cfg.HugoUpgrade.BinaryLink)
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		return fmt.Errorf("permission_denied: managed Hugo link directory could not be created")
	}
	if err := requireRealPath(filepath.Dir(link)); err != nil {
		return err
	}
	relativeTarget, err := filepath.Rel(filepath.Dir(link), target)
	if err != nil {
		return fmt.Errorf("activation_error: managed target could not be made relative")
	}
	tmp := filepath.Join(filepath.Dir(link), ".hugo-link-"+fmt.Sprint(time.Now().UnixNano()))
	if err := os.Symlink(relativeTarget, tmp); err != nil {
		return fmt.Errorf("permission_denied: temporary managed Hugo symlink could not be created")
	}
	defer os.Remove(tmp)
	if err := os.Rename(tmp, link); err != nil {
		return fmt.Errorf("activation_error: managed Hugo symlink could not be replaced atomically")
	}
	return nil
}

func (m *hugoUpgradeManager) restoreManagedLink(targetRel string) error {
	if targetRel == "" {
		return os.Remove(m.cfg.HugoUpgrade.BinaryLink)
	}
	return m.swapManagedLink(targetRel)
}

func (m *hugoUpgradeManager) loadActivationRecord() (hugoActivationRecord, error) {
	var record hugoActivationRecord
	path := filepath.Join(m.cfg.HugoUpgrade.ManagedDir, hugoActivationFilename)
	if err := requireRegularFile(path); err != nil {
		return record, fmt.Errorf("rollback_unavailable: no valid managed Hugo activation record exists")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return record, fmt.Errorf("rollback_unavailable: no managed Hugo activation record exists")
	}
	if json.Unmarshal(raw, &record) != nil || normalizeVersion(record.ActiveVersion) == "" || record.ActiveTarget == "" ||
		record.PreviousTarget == "" || normalizeVersion(record.PreviousVersion) == "" || len(record.PreviousChecksum) != sha256.Size*2 {
		return record, fmt.Errorf("rollback_unavailable: managed Hugo activation record is invalid")
	}
	return record, nil
}

func (m *hugoUpgradeManager) audit(ctx context.Context, tool, result, version, checksum string, err error) {
	actor := caller.Key(ctx)
	if actor == "" {
		actor = currentUserForLog()
	}
	attrs := []any{"tool", tool, "actor", actor}
	if version != "" {
		attrs = append(attrs, "target_version", version)
	}
	if checksum != "" {
		attrs = append(attrs, "checksum", checksum)
	}
	if err != nil {
		attrs = append(attrs, "error_code", strings.SplitN(err.Error(), ":", 2)[0])
		audit.Warn(audit.EventAdminOperation, result, attrs...)
		return
	}
	audit.Info(audit.EventAdminOperation, result, attrs...)
}
