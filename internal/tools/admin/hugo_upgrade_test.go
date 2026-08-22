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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func boolPointer(v bool) *bool { return &v }

func fakeHugoScript(version string) []byte {
	return []byte("#!/bin/sh\nprintf 'hugo " + version + "+extended linux/amd64 BuildDate=test\\n'\n")
}

func fakeHugoArchive(t *testing.T, version string, unsafePath bool) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	name := "hugo"
	if unsafePath {
		name = "../hugo"
	}
	body := fakeHugoScript(version)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o750, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type fakeHugoReleaseServer struct {
	server           *httptest.Server
	archive          []byte
	manifest         []byte
	version          string
	archiveRequests  atomic.Int32
	manifestRequests atomic.Int32
}

func newFakeHugoReleaseServer(t *testing.T, version string, archive []byte, manifestOverride []byte) *fakeHugoReleaseServer {
	t.Helper()
	f := &fakeHugoReleaseServer{version: version, archive: archive}
	sum := sha256.Sum256(archive)
	assetName := hugoArchiveName(version, true, "linux", "amd64")
	f.manifest = []byte(hex.EncodeToString(sum[:]) + "  " + assetName + "\n")
	if manifestOverride != nil {
		f.manifest = manifestOverride
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		release := hugoRelease{
			TagName: version, PublishedAt: time.Date(2026, 8, 10, 1, 2, 3, 0, time.UTC),
			Assets: []hugoReleaseAsset{
				{Name: assetName, URL: base + "/assets/archive"},
				{Name: "hugo_" + strings.TrimPrefix(version, "v") + "_checksums.txt", URL: base + "/assets/checksums"},
			},
		}
		switch r.URL.Path {
		case "/releases/latest", "/releases/tags/" + version:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(release)
		case "/assets/archive":
			f.archiveRequests.Add(1)
			_, _ = w.Write(f.archive)
		case "/assets/checksums":
			f.manifestRequests.Add(1)
			_, _ = w.Write(f.manifest)
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func (f *fakeHugoReleaseServer) Close() { f.server.Close() }

func testHugoUpgradeConfig(t *testing.T, releaseURL string) config.Config {
	t.Helper()
	u, err := url.Parse(releaseURL)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	cfg := config.Default()
	cfg.HugoUpgrade.Enabled = true
	cfg.HugoUpgrade.ManagedDir = root
	cfg.HugoUpgrade.BinaryLink = filepath.Join(root, "current", "hugo")
	cfg.HugoUpgrade.ReleaseAPIBaseURL = releaseURL
	cfg.HugoUpgrade.AllowedHosts = []string{u.Hostname()}
	cfg.HugoUpgrade.MaxDownloadBytes = 4 * 1024 * 1024
	cfg.HugoUpgrade.CacheTTLSeconds = 3600
	cfg.HugoUpgrade.RequireExtended = true
	return cfg
}

func installFakeCurrentHugo(t *testing.T, version string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hugo"), fakeHugoScript(version), 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestHugoUpdateStatusMakesNoNetworkRequestByDefault(t *testing.T) {
	cfg := config.Default()
	mgr := newHugoUpgradeManager(cfg)
	var calls atomic.Int32
	mgr.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("network must not be called")
	})
	if _, err := mgr.status(context.Background(), false); err != nil {
		t.Fatalf("status(false): %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("network calls = %d, want 0", calls.Load())
	}
}

func TestHugoUpdateStatusChecksLatestAndUsesCache(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	fake := newFakeHugoReleaseServer(t, "v9.9.9", fakeHugoArchive(t, "v9.9.9", false), nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)

	first, err := mgr.status(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if first.Latest == nil || first.Latest.Version != "9.9.9" || !first.Latest.UpdateAvailable || !first.Latest.Compatible || !first.NetworkChecked {
		t.Fatalf("first status = %#v", first)
	}
	second, err := mgr.status(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if second.Latest == nil || second.Latest.Source != "cache" || second.NetworkChecked {
		t.Fatalf("cached status = %#v", second)
	}
}

func TestComparableInstalledVersionIgnoresHugoBuildSuffix(t *testing.T) {
	got := comparableInstalledVersion("0.147.0-7d0039b86ddd+extended linux/amd64")
	if got != "v0.147.0" {
		t.Fatalf("comparable installed version = %q", got)
	}
}

func TestHugoUpgradeFullStageActivateRollback(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	archive := fakeHugoArchive(t, "v9.9.9", false)
	fake := newFakeHugoReleaseServer(t, "v9.9.9", archive, nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)

	// Seed an explicitly managed previous target. Activation must preserve it
	// and rollback must restore this exact symlink target.
	oldDir := filepath.Join(cfg.HugoUpgrade.ManagedDir, "versions", "v1.0.0")
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "hugo"), fakeHugoScript("v1.0.0"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.HugoUpgrade.BinaryLink), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "versions", "v1.0.0", "hugo"), cfg.HugoUpgrade.BinaryLink); err != nil {
		t.Fatal(err)
	}

	dry, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "9.9.9"})
	if err != nil || !dry.DryRun || dry.Staged {
		t.Fatalf("default dry-run = %#v, %v", dry, err)
	}
	if fake.archiveRequests.Load() != 0 || fake.manifestRequests.Load() != 0 {
		t.Fatal("dry-run must not download release assets")
	}
	real, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if !real.Staged || real.Checksum == "" || !real.ChecksumVerified || !real.VersionVerified ||
		!real.ActivationRestartRequired || real.RollbackTarget != "current_managed_target_at_activation" || !real.RollbackPreservedOnActivation {
		t.Fatalf("stage result = %#v", real)
	}

	activationDry, err := mgr.activate(context.Background(), activateHugoUpgradeInput{TargetVersion: "v9.9.9"})
	if err != nil || !activationDry.DryRun || activationDry.Activated {
		t.Fatalf("activation dry-run = %#v, %v", activationDry, err)
	}
	before, _ := os.Readlink(cfg.HugoUpgrade.BinaryLink)
	if !strings.Contains(filepath.ToSlash(before), "v1.0.0") {
		t.Fatalf("dry-run changed link to %q", before)
	}
	activated, err := mgr.activate(context.Background(), activateHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
	if err != nil || !activated.Activated || activated.PreviousVersion != "v1.0.0" || activated.Checksum == "" {
		t.Fatalf("activate = %#v, %v", activated, err)
	}
	active, _, err := mgr.currentManagedTarget()
	if err != nil || active != "versions/v9.9.9/hugo" {
		t.Fatalf("active target = %q, %v", active, err)
	}
	rolled, err := mgr.rollback(context.Background(), rollbackHugoUpgradeInput{DryRun: boolPointer(false)})
	if err != nil || !rolled.RolledBack || rolled.RestoredVersion != "v1.0.0" || rolled.Checksum == "" {
		t.Fatalf("rollback = %#v, %v", rolled, err)
	}
	restored, _, err := mgr.currentManagedTarget()
	if err != nil || restored != "versions/v1.0.0/hugo" {
		t.Fatalf("restored target = %q, %v", restored, err)
	}
}

func TestHugoActivationRecordFailureRestoresManagedLink(t *testing.T) {
	for _, tc := range []struct {
		name             string
		withPrevious     bool
		wantTarget       string
		wantLinkToRemain bool
	}{
		{name: "restore previous target", withPrevious: true, wantTarget: "versions/v1.0.0/hugo", wantLinkToRemain: true},
		{name: "remove first activation link", withPrevious: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installFakeCurrentHugo(t, "v1.0.0")
			fake := newFakeHugoReleaseServer(t, "v9.9.9", fakeHugoArchive(t, "v9.9.9", false), nil)
			defer fake.Close()
			cfg := testHugoUpgradeConfig(t, fake.server.URL)
			mgr := newHugoUpgradeManager(cfg)

			if tc.withPrevious {
				oldDir := filepath.Join(cfg.HugoUpgrade.ManagedDir, "versions", "v1.0.0")
				if err := os.MkdirAll(oldDir, 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(oldDir, "hugo"), fakeHugoScript("v1.0.0"), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(cfg.HugoUpgrade.BinaryLink), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join("..", "versions", "v1.0.0", "hugo"), cfg.HugoUpgrade.BinaryLink); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)}); err != nil {
				t.Fatalf("stage: %v", err)
			}
			// AtomicWrite cannot rename its temporary file over a directory. The
			// symlink swap has already happened at that point, forcing activate's
			// compensating restoreManagedLink path deterministically.
			if err := os.Mkdir(filepath.Join(cfg.HugoUpgrade.ManagedDir, hugoActivationFilename), 0o750); err != nil {
				t.Fatal(err)
			}

			got, err := mgr.activate(context.Background(), activateHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
			if err == nil || !strings.Contains(err.Error(), "activation_error") {
				t.Fatalf("activate = %#v, %v; want activation_error", got, err)
			}
			if got.Activated {
				t.Fatalf("failed activation reported success: %#v", got)
			}

			if !tc.wantLinkToRemain {
				if _, statErr := os.Lstat(cfg.HugoUpgrade.BinaryLink); !os.IsNotExist(statErr) {
					t.Fatalf("failed first activation left managed link behind: %v", statErr)
				}
				return
			}
			restored, _, targetErr := mgr.currentManagedTarget()
			if targetErr != nil || restored != tc.wantTarget {
				t.Fatalf("managed target after failed activation = %q, %v; want %q", restored, targetErr, tc.wantTarget)
			}
		})
	}
}

func TestHugoStageRejectsChecksumMismatchWithoutArtifact(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	archive := fakeHugoArchive(t, "v9.9.9", false)
	manifest := []byte(strings.Repeat("0", 64) + "  " + hugoArchiveName("v9.9.9", true, "linux", "amd64") + "\n")
	fake := newFakeHugoReleaseServer(t, "v9.9.9", archive, manifest)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)
	_, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "checksum_mismatch") {
		t.Fatalf("stage error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.HugoUpgrade.ManagedDir, "versions", "v9.9.9")); !os.IsNotExist(statErr) {
		t.Fatalf("checksum failure left staged artifact: %v", statErr)
	}
}

func TestHugoStageRejectsArchiveTraversal(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	archive := fakeHugoArchive(t, "v9.9.9", true)
	fake := newFakeHugoReleaseServer(t, "v9.9.9", archive, nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	_, err := newHugoUpgradeManager(cfg).stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("stage traversal error = %v", err)
	}
}

func TestHugoStageRejectsWrongExecutableVersion(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	archive := fakeHugoArchive(t, "v8.8.8", false)
	fake := newFakeHugoReleaseServer(t, "v9.9.9", archive, nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	_, err := newHugoUpgradeManager(cfg).stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "version_mismatch") {
		t.Fatalf("wrong executable version error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(cfg.HugoUpgrade.ManagedDir, "versions", "v9.9.9")); !os.IsNotExist(statErr) {
		t.Fatalf("wrong version left staged artifact: %v", statErr)
	}
}

func TestHugoStageRejectsMalformedManifest(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	archive := fakeHugoArchive(t, "v9.9.9", false)
	fake := newFakeHugoReleaseServer(t, "v9.9.9", archive, []byte("not-a-checksum  wrong-file\n"))
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	_, err := newHugoUpgradeManager(cfg).stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("malformed manifest error = %v", err)
	}
}

func TestHugoStageRejectsDisabledAndDowngrade(t *testing.T) {
	cfg := config.Default()
	if _, err := newHugoUpgradeManager(cfg).stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled stage error = %v", err)
	}

	installFakeCurrentHugo(t, "v9.9.9")
	fake := newFakeHugoReleaseServer(t, "v1.0.0", fakeHugoArchive(t, "v1.0.0", false), nil)
	defer fake.Close()
	cfg = testHugoUpgradeConfig(t, fake.server.URL)
	if _, err := newHugoUpgradeManager(cfg).stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v1.0.0"}); err == nil || !strings.Contains(err.Error(), "downgrade_forbidden") {
		t.Fatalf("downgrade error = %v", err)
	}
}

func TestHugoStageEnforcesConfiguredVersionPolicyBeforeNetwork(t *testing.T) {
	cfg := config.Default()
	cfg.HugoUpgrade.Enabled = true
	cfg.HugoUpgrade.MinimumVersion = "v2.0.0"
	cfg.HugoUpgrade.MaximumVersion = "v3.0.0"
	mgr := newHugoUpgradeManager(cfg)
	var calls atomic.Int32
	mgr.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("unexpected network")
	})
	if _, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v1.0.0"}); err == nil || !strings.Contains(err.Error(), "version_policy_denied") {
		t.Fatalf("minimum policy error = %v", err)
	}
	if _, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v4.0.0"}); err == nil || !strings.Contains(err.Error(), "version_policy_denied") {
		t.Fatalf("maximum policy error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("policy rejection made %d network calls", calls.Load())
	}
}

func TestHugoDownloadRejectsRedirectToUnlistedHost(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost/blocked", http.StatusFound)
	}))
	defer redirect.Close()
	u, _ := url.Parse(redirect.URL)
	cfg := config.Default()
	cfg.HugoUpgrade.ReleaseAPIBaseURL = redirect.URL
	cfg.HugoUpgrade.AllowedHosts = []string{u.Hostname()}
	mgr := newHugoUpgradeManager(cfg)
	if _, err := mgr.download(context.Background(), redirect.URL, 1024); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestHugoDownloadIsBoundedAndTimesOut(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = w.Write(bytes.Repeat([]byte("x"), 2048))
			return
		}
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer slow.Close()
	u, _ := url.Parse(slow.URL)
	cfg := config.Default()
	cfg.HugoUpgrade.ReleaseAPIBaseURL = slow.URL
	cfg.HugoUpgrade.AllowedHosts = []string{u.Hostname()}
	mgr := newHugoUpgradeManager(cfg)
	if _, err := mgr.download(context.Background(), slow.URL+"/large", 1024); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("bounded download error = %v", err)
	}
	mgr.client.Timeout = 10 * time.Millisecond
	if _, err := mgr.download(context.Background(), slow.URL+"/slow", 1024); err == nil || !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestHugoActivationRejectsTamperedStagedBinary(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	archive := fakeHugoArchive(t, "v9.9.9", false)
	fake := newFakeHugoReleaseServer(t, "v9.9.9", archive, nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)
	if _, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(cfg.HugoUpgrade.ManagedDir, "versions", "v9.9.9", "hugo")
	if err := os.WriteFile(binary, fakeHugoScript("v9.9.8"), 0o750); err != nil {
		t.Fatal(err)
	}
	_, err := mgr.activate(context.Background(), activateHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "checksum_mismatch") {
		t.Fatalf("tampered activation error = %v", err)
	}
	if _, statErr := os.Lstat(cfg.HugoUpgrade.BinaryLink); !os.IsNotExist(statErr) {
		t.Fatalf("tampered activation created link: %v", statErr)
	}
}

func TestHugoRollbackRejectsTamperedPreviousBinary(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	archive := fakeHugoArchive(t, "v9.9.9", false)
	fake := newFakeHugoReleaseServer(t, "v9.9.9", archive, nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)

	oldDir := filepath.Join(cfg.HugoUpgrade.ManagedDir, "versions", "v1.0.0")
	if err := os.MkdirAll(filepath.Dir(cfg.HugoUpgrade.BinaryLink), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(oldDir, 0o750); err != nil {
		t.Fatal(err)
	}
	oldBinary := filepath.Join(oldDir, "hugo")
	if err := os.WriteFile(oldBinary, fakeHugoScript("v1.0.0"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "versions", "v1.0.0", "hugo"), cfg.HugoUpgrade.BinaryLink); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.activate(context.Background(), activateHugoUpgradeInput{TargetVersion: "v9.9.9", DryRun: boolPointer(false)}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldBinary, append(fakeHugoScript("v1.0.0"), []byte("# tampered\n")...), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.rollback(context.Background(), rollbackHugoUpgradeInput{DryRun: boolPointer(false)}); err == nil || !strings.Contains(err.Error(), "checksum_mismatch") {
		t.Fatalf("tampered rollback error = %v", err)
	}
	active, _, err := mgr.currentManagedTarget()
	if err != nil || active != "versions/v9.9.9/hugo" {
		t.Fatalf("failed rollback changed active target to %q: %v", active, err)
	}
}

func TestHugoUpgradeRejectsSymlinkedManagedRoot(t *testing.T) {
	realRoot := t.TempDir()
	parent := t.TempDir()
	symlinkRoot := filepath.Join(parent, "managed")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.HugoUpgrade.Enabled = true
	cfg.HugoUpgrade.ManagedDir = symlinkRoot
	cfg.HugoUpgrade.BinaryLink = filepath.Join(symlinkRoot, "current", "hugo")
	if err := newHugoUpgradeManager(cfg).ensureManagedRoot(); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked managed root error = %v", err)
	}
}

func TestHugoUpgradeAuditUsesStableCallerAndNoSecret(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(old)
	ctx := context.WithValue(context.Background(), oauth.CtxPrincipal, "writer-client-a")
	newHugoUpgradeManager(config.Default()).audit(ctx, "activate_hugo", "success", "v9.9.9", strings.Repeat("a", 64), nil)
	logLine := buf.String()
	if !strings.Contains(logLine, `"actor":"writer-client-a"`) || !strings.Contains(logLine, `"target_version":"v9.9.9"`) {
		t.Fatalf("audit event missing stable actor/version: %s", logLine)
	}
	if strings.Contains(logLine, "Authorization") || strings.Contains(logLine, "Bearer ") {
		t.Fatalf("audit event leaked credentials: %s", logLine)
	}
}

// TestHugoBootstrapDetectsStagesActivatesAndUnblocksRollback is the full
// end-to-end guard for #980's rollback-bootstrap gap: on a fresh deployment
// (no managed version ever activated), rollback_hugo has nothing to restore
// on the very first real activation. bootstrap_hugo re-downloads and
// checksum-verifies the currently-installed version, activates it as the
// initial managed baseline, and only *then* does the first real upgrade's
// activation get a legitimate previous_target for rollback_hugo to use.
func TestHugoBootstrapDetectsStagesActivatesAndUnblocksRollback(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	fake := newFakeHugoReleaseServer(t, "v1.0.0", fakeHugoArchive(t, "v1.0.0", false), nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)

	got, err := mgr.bootstrap(context.Background(), bootstrapHugoInput{DryRun: boolPointer(false)})
	if err != nil {
		t.Fatalf("bootstrap() error = %v", err)
	}
	if got.DetectedVersion != "v1.0.0" || !got.Staged || !got.Activated || got.Checksum == "" {
		t.Fatalf("bootstrap() = %#v, want detected/staged/activated v1.0.0", got)
	}
	active, activeVersion, err := mgr.currentManagedTarget()
	if err != nil || activeVersion != "v1.0.0" {
		t.Fatalf("currentManagedTarget() after bootstrap = (%q, %q, %v)", active, activeVersion, err)
	}

	// Rollback must still correctly refuse: bootstrap created a baseline,
	// but there is still no *second* activation to roll back to yet.
	if _, err := mgr.rollback(context.Background(), rollbackHugoUpgradeInput{DryRun: boolPointer(false)}); err == nil || !strings.Contains(err.Error(), "rollback_unavailable") {
		t.Fatalf("rollback immediately after bootstrap = %v, want rollback_unavailable", err)
	}

	// Now perform the first real upgrade after bootstrapping. Reuse a fresh
	// server pinned to a new version so stage/activate resolve against it.
	fakeV2 := newFakeHugoReleaseServer(t, "v2.0.0", fakeHugoArchive(t, "v2.0.0", false), nil)
	defer fakeV2.Close()
	mgr.cfg.HugoUpgrade.ReleaseAPIBaseURL = fakeV2.server.URL
	u, err := url.Parse(fakeV2.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	mgr.cfg.HugoUpgrade.AllowedHosts = []string{u.Hostname()}
	if _, err := mgr.stage(context.Background(), stageHugoUpgradeInput{TargetVersion: "v2.0.0", DryRun: boolPointer(false)}); err != nil {
		t.Fatalf("stage(v2.0.0) error = %v", err)
	}
	if _, err := mgr.activate(context.Background(), activateHugoUpgradeInput{TargetVersion: "v2.0.0", DryRun: boolPointer(false)}); err != nil {
		t.Fatalf("activate(v2.0.0) error = %v", err)
	}

	// This is the whole point: rollback now has a legitimate target,
	// because bootstrap gave the *first* activation a real previous_target.
	rolledBack, err := mgr.rollback(context.Background(), rollbackHugoUpgradeInput{DryRun: boolPointer(false)})
	if err != nil {
		t.Fatalf("rollback after bootstrap+upgrade error = %v", err)
	}
	if !rolledBack.RolledBack || rolledBack.RestoredVersion != "v1.0.0" {
		t.Fatalf("rollback() = %#v, want restored v1.0.0", rolledBack)
	}
}

func TestHugoBootstrapRefusesWhenAlreadyManaged(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	fake := newFakeHugoReleaseServer(t, "v1.0.0", fakeHugoArchive(t, "v1.0.0", false), nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)

	if _, err := mgr.bootstrap(context.Background(), bootstrapHugoInput{DryRun: boolPointer(false)}); err != nil {
		t.Fatalf("first bootstrap() error = %v", err)
	}
	_, err := mgr.bootstrap(context.Background(), bootstrapHugoInput{DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "bootstrap_unavailable") {
		t.Fatalf("second bootstrap() error = %v, want bootstrap_unavailable", err)
	}
}

// TestHugoBootstrapRefusesExtendedVariantMismatch guards the gap advisor
// flagged in review: bootstrap must compare the installed binary's extended
// variant against hugo_upgrade.require_extended before staging, because
// stageLocked picks the release asset from config alone and would otherwise
// surface a confusing version_mismatch instead of the real cause.
func TestHugoBootstrapRefusesExtendedVariantMismatch(t *testing.T) {
	dir := t.TempDir()
	nonExtended := []byte("#!/bin/sh\nprintf 'hugo v1.0.0 linux/amd64 BuildDate=test\\n'\n")
	if err := os.WriteFile(filepath.Join(dir, "hugo"), nonExtended, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	fake := newFakeHugoReleaseServer(t, "v1.0.0", fakeHugoArchive(t, "v1.0.0", false), nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	cfg.HugoUpgrade.RequireExtended = true
	mgr := newHugoUpgradeManager(cfg)

	_, err := mgr.bootstrap(context.Background(), bootstrapHugoInput{DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "config_error") || !strings.Contains(err.Error(), "extended") {
		t.Fatalf("bootstrap() with extended mismatch error = %v, want config_error mentioning extended", err)
	}
	if fake.archiveRequests.Load() != 0 {
		t.Fatalf("bootstrap() with extended mismatch made an archive request, want none")
	}
}

func TestHugoBootstrapDryRunMakesNoNetworkRequestOrWrite(t *testing.T) {
	installFakeCurrentHugo(t, "v1.0.0")
	fake := newFakeHugoReleaseServer(t, "v1.0.0", fakeHugoArchive(t, "v1.0.0", false), nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	mgr := newHugoUpgradeManager(cfg)

	got, err := mgr.bootstrap(context.Background(), bootstrapHugoInput{})
	if err != nil {
		t.Fatalf("bootstrap(dry_run default) error = %v", err)
	}
	if !got.DryRun || got.Staged || got.Activated || got.DetectedVersion != "v1.0.0" {
		t.Fatalf("dry-run bootstrap() = %#v, want dry_run detection only", got)
	}
	if fake.archiveRequests.Load() != 0 || fake.manifestRequests.Load() != 0 {
		t.Fatalf("dry-run bootstrap made network requests: archive=%d manifest=%d", fake.archiveRequests.Load(), fake.manifestRequests.Load())
	}
	if _, _, err := mgr.currentManagedTarget(); err != nil {
		t.Fatalf("currentManagedTarget() after dry-run bootstrap error = %v", err)
	}
	if active, _, _ := mgr.currentManagedTarget(); active != "" {
		t.Fatalf("dry-run bootstrap created a managed target: %q", active)
	}
}

func TestHugoBootstrapFailsWithoutInstalledHugo(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	fake := newFakeHugoReleaseServer(t, "v1.0.0", fakeHugoArchive(t, "v1.0.0", false), nil)
	defer fake.Close()
	cfg := testHugoUpgradeConfig(t, fake.server.URL)
	_, err := newHugoUpgradeManager(cfg).bootstrap(context.Background(), bootstrapHugoInput{DryRun: boolPointer(false)})
	if err == nil || !strings.Contains(err.Error(), "config_error") {
		t.Fatalf("bootstrap() without installed hugo error = %v, want config_error", err)
	}
}
