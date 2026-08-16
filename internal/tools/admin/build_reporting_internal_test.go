package admin

import (
	"context"
	gobuildinfo "debug/buildinfo"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestFormatHugoBuildVersionFromBinaryMetadata(t *testing.T) {
	info := &gobuildinfo.BuildInfo{
		Main:     debug.Module{Path: "github.com/gohugoio/hugo", Version: "v0.164.0"},
		Settings: []debug.BuildSetting{{Key: "-tags", Value: "extended,nodeploy"}},
	}
	if got := formatHugoBuildVersion(info); got != "0.164.0+extended" {
		t.Fatalf("formatHugoBuildVersion() = %q", got)
	}
	for _, invalid := range []*gobuildinfo.BuildInfo{nil, {}, {Main: debug.Module{Path: "example.test/wrapper", Version: "v1.0.0"}}, {Main: debug.Module{Path: "github.com/gohugoio/hugo", Version: "(devel)"}}} {
		if got := formatHugoBuildVersion(invalid); got != "" {
			t.Fatalf("formatHugoBuildVersion(%#v) = %q, want empty", invalid, got)
		}
	}
}

func TestRunBuildPreparationFailuresFinalizeDurableIntent(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "hugo"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	cfg := config.Default()
	cfg.HugoRoot = t.TempDir()
	cfg.SiteRoot = filepath.Join(t.TempDir(), "public")
	if err := os.MkdirAll(cfg.SiteRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		callback PostBuildCallback
		want     string
	}{
		{name: "prepare", callback: PostBuildCallback{Name: "build_pages", OnBuildPrepared: func(BuildProgress) ([]BuildPageChange, error) {
			return nil, errors.New("injected prepare failure")
		}}, want: "build_reconciliation_failed"},
		{name: "start", callback: PostBuildCallback{Name: "recovery", OnBuildStart: func(BuildProgress) error {
			return errors.New("injected recovery failure")
		}}, want: "build_recovery_record_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failedState := ""
			observer := PostBuildCallback{Name: "observer", OnBuildFailed: func(_ BuildProgress, state string) error {
				failedState = state
				return nil
			}}
			_, err := runBuild(context.Background(), cfg, nil, "", "", tc.callback, observer)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("runBuild error=%v, want %s", err, tc.want)
			}
			if failedState == "" {
				t.Fatal("OnBuildFailed was not called for durable pre-build intent")
			}
		})
	}
}

// TestClassifyPendingPages covers #858 AC1/AC3: the changed set is split into
// published (included) vs draft/test-content (excluded_drafts), with stable
// "slug" / "slug:lang" identifiers, and deleted_outputs is always a non-nil
// (documented empty) slice.
func TestClassifyPendingPages(t *testing.T) {
	pending := []hugosite.SourcePage{
		{Slug: "posts/example", Lang: "fr"},                                                      // normal → included
		{Slug: "posts/example", Lang: "en", Draft: true},                                         // draft → excluded
		{Slug: "posts/beta", Lang: "", Draft: false},                                             // default lang, normal
		{Slug: "posts/secret", Lang: "fr", FrontmatterRaw: map[string]any{"test_content": true}}, // test_content → excluded
	}
	got := classifyPendingPages(pending)

	wantIncluded := []string{"posts/beta", "posts/example:fr"}
	wantExcluded := []string{"posts/example:en", "posts/secret:fr"}
	if !reflect.DeepEqual(got.Included, wantIncluded) {
		t.Fatalf("included = %v, want %v", got.Included, wantIncluded)
	}
	if !reflect.DeepEqual(got.ExcludedDrafts, wantExcluded) {
		t.Fatalf("excluded_drafts = %v, want %v", got.ExcludedDrafts, wantExcluded)
	}
	if got.DeletedOutputs == nil || len(got.DeletedOutputs) != 0 {
		t.Fatalf("deleted_outputs = %v, want non-nil empty", got.DeletedOutputs)
	}
}

func TestClassifyPendingPagesEmpty(t *testing.T) {
	got := classifyPendingPages(nil)
	if len(got.Included) != 0 || len(got.ExcludedDrafts) != 0 || got.Included == nil || got.ExcludedDrafts == nil {
		t.Fatalf("empty changed set should yield non-nil empty slices, got %+v", got)
	}
}

func TestClassifyBuildPageChangesIncludesDurableDeletionAndDraftState(t *testing.T) {
	got := classifyBuildPageChanges([]BuildPageChange{
		{SourceKey: "posts/live", Lang: "fr"},
		{SourceKey: "posts/wip", Lang: "en", Draft: true},
		{SourceKey: "posts/test", Lang: "fr", TestContent: true},
		{SourceKey: "posts/removed", Lang: "en", Deleted: true},
	})
	wantIncluded := []string{"posts/live:fr"}
	wantExcluded := []string{"posts/test:fr", "posts/wip:en"}
	wantDeleted := []string{"posts/removed:en"}
	if !reflect.DeepEqual(got.Included, wantIncluded) || !reflect.DeepEqual(got.ExcludedDrafts, wantExcluded) || !reflect.DeepEqual(got.DeletedOutputs, wantDeleted) {
		t.Fatalf("classifyBuildPageChanges = %#v, want included=%v excluded=%v deleted=%v", got, wantIncluded, wantExcluded, wantDeleted)
	}
}

// TestBuildStages covers #858 AC2: stage-by-stage status derived from the
// per-callback outcomes, including the callback-free (skipped) and
// required-callback-failure (partial_failure) cases from AC4.
func TestBuildStages(t *testing.T) {
	cases := []struct {
		name       string
		outcomes   map[string]string
		wantReload string
		wantStatus string
	}{
		{"callback-free", map[string]string{}, "skipped", "skipped"},
		{"all ok", map[string]string{"index_reload": "ok", "db_reindex": "ok"}, "ok", "ok"},
		{"required failure", map[string]string{"index_reload": "failed", "db_reindex": "ok"}, "failed", "partial_failure"},
		{"timeout", map[string]string{"index_reload": "timeout"}, "timeout", "partial_failure"},
		{"advisory ok only", map[string]string{"cloudflare_purge": "ok"}, "skipped", "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := buildStages(tc.outcomes)
			if st.HugoBuild != "ok" {
				t.Errorf("hugo_build = %q, want ok", st.HugoBuild)
			}
			if st.SourceIndexReload != tc.wantReload || st.PublicIndexReload != tc.wantReload {
				t.Errorf("index reload stages = (%q,%q), want %q", st.SourceIndexReload, st.PublicIndexReload, tc.wantReload)
			}
			if st.CallbacksStatus != tc.wantStatus {
				t.Errorf("callbacks_status = %q, want %q", st.CallbacksStatus, tc.wantStatus)
			}
		})
	}
}
