package anonymous_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	writepkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
)

// TestGetCapabilitiesReportsLimitsAndFeatureFlags is the #859 contract test:
// get_capabilities must expose the hard limits (from the write tools' own
// source of truth, so the two can't drift) and coarse feature flags, and
// must NOT leak sensitive config (secrets, hook command strings, host paths).
func TestGetCapabilitiesReportsLimitsAndFeatureFlags(t *testing.T) {
	idx := mustTestIndex(t)

	cfg := config.Default()
	cfg.DefaultLanguage = "en"
	cfg.BlockedShortcodes = []string{"raw", "script", "style"}
	cfg.PostBuildHooks = []string{"/usr/local/bin/deploy.sh --prod"} // must NOT appear verbatim
	cfg.ImageGenURL = "https://images.example/generate"
	cfg.RateLimit.CreateUpdatePerMin = 42
	cfg.RateLimit.DestructivePerMin = 7

	session, done := newTestClientWithCfg(t, idx, cfg, nil)
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	data := decodeContent(t, res)

	limits, ok := data["limits"].(map[string]any)
	if !ok {
		t.Fatalf("limits missing/wrong type: %#v", data["limits"])
	}
	if got, want := int(limits["body_max_bytes"].(float64)), writepkg.BodyMaxBytes(); got != want {
		t.Errorf("body_max_bytes = %d, want %d (write source of truth)", got, want)
	}
	if got, want := int(limits["asset_max_bytes"].(float64)), writepkg.AssetMaxBytes(); got != want {
		t.Errorf("asset_max_bytes = %d, want %d", got, want)
	}
	rl, ok := limits["rate_limits"].(map[string]any)
	if !ok {
		t.Fatalf("rate_limits missing: %#v", limits["rate_limits"])
	}
	if got := int(rl["create_update_upload_per_min"].(float64)); got != 42 {
		t.Errorf("create_update_upload_per_min = %d, want 42", got)
	}
	if got := int(rl["destructive_per_min"].(float64)); got != 7 {
		t.Errorf("destructive_per_min = %d, want 7", got)
	}
	ttl, ok := limits["preview_ttl"].(map[string]any)
	if !ok || ttl["min_seconds"] == nil || ttl["max_seconds"] == nil {
		t.Errorf("preview_ttl bounds missing: %#v", limits["preview_ttl"])
	}

	formats, ok := data["allowed_image_formats"].([]any)
	if !ok || len(formats) == 0 {
		t.Fatalf("allowed_image_formats missing/empty: %#v", data["allowed_image_formats"])
	}

	features, ok := data["features"].(map[string]any)
	if !ok {
		t.Fatalf("features missing: %#v", data["features"])
	}
	if features["image_generation_available"] != true {
		t.Errorf("image_generation_available = %v, want true (ImageGenURL set)", features["image_generation_available"])
	}
	if features["external_image_generation_available"] != true {
		t.Errorf("external_image_generation_available = %v, want true (ImageGenURL set)", features["external_image_generation_available"])
	}
	if features["local_hero_generation_available"] != false {
		t.Errorf("local_hero_generation_available = %v, want false when HugoRoot is empty in this test config", features["local_hero_generation_available"])
	}
	if features["post_build_hooks_configured"] != true {
		t.Errorf("post_build_hooks_configured = %v, want true", features["post_build_hooks_configured"])
	}
	if got := int(features["post_build_hooks_count"].(float64)); got != 1 {
		t.Errorf("post_build_hooks_count = %d, want 1", got)
	}

	// Security invariant: the actual hook command must never be exposed.
	env := decodeEnvelope(t, res)
	if leaks := findStringLeak(env, "deploy.sh"); leaks {
		t.Errorf("get_capabilities leaked a post-build hook command string")
	}
	if leaks := findStringLeak(env, "images.example"); leaks {
		t.Errorf("get_capabilities leaked the image-generation URL")
	}
}

// TestGetCapabilitiesAdvertisesConfiguredLanguagesWhenSet is a regression
// test for #899: when the operator sets cfg.ConfiguredLanguages,
// get_capabilities.languages.available must report exactly that
// authoritative set — including a configured-but-still-content-empty
// language — rather than the derived-from-index set, so "advertised ==
// enforced" holds against create_page's rejectUnconfiguredLang.
func TestGetCapabilitiesAdvertisesConfiguredLanguagesWhenSet(t *testing.T) {
	idx := mustTestIndex(t)

	cfg := config.Default()
	cfg.DefaultLanguage = "en"
	cfg.ConfiguredLanguages = []string{"en", "fr", "de"}

	session, done := newTestClientWithCfg(t, idx, cfg, nil)
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	data := decodeContent(t, res)
	languages, ok := data["languages"].(map[string]any)
	if !ok {
		t.Fatalf("languages missing/wrong type: %#v", data["languages"])
	}
	available, ok := languages["available"].([]any)
	if !ok {
		t.Fatalf("languages.available missing/wrong type: %#v", languages["available"])
	}
	if got := languages["mode"]; got != "configured" {
		t.Fatalf("languages.mode = %v, want configured", got)
	}
	got := make([]string, len(available))
	for i, v := range available {
		got[i], _ = v.(string)
	}
	want := []string{"de", "en", "fr"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("languages.available = %v, want exactly configured_languages %v (including the configured-but-content-empty \"de\")", got, want)
	}
}

func TestGetCapabilitiesReportsObservedLanguageModeAndLocalHeroGeneration(t *testing.T) {
	idx := mustTestIndex(t)

	cfg := config.Default()
	cfg.DefaultLanguage = "en"
	cfg.HugoRoot = t.TempDir()

	session, done := newTestClientWithCfg(t, idx, cfg, nil)
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	data := decodeContent(t, res)
	languages := data["languages"].(map[string]any)
	if got := languages["mode"]; got != "observed" {
		t.Fatalf("languages.mode = %v, want observed when configured_languages is empty", got)
	}
	features := data["features"].(map[string]any)
	if got := features["local_hero_generation_available"]; got != true {
		t.Fatalf("local_hero_generation_available = %v, want true when HugoRoot is configured", got)
	}
	if got := features["image_generation_available"]; got != true {
		t.Fatalf("image_generation_available = %v, want true when local hero generation is available", got)
	}
}

func TestGetCapabilitiesObservedLanguagesComeFromSourceWhenPublicBuildIsPartial(t *testing.T) {
	publicRoot := t.TempDir()
	publicPath := filepath.Join(publicRoot, "posts", "hello", "index.html")
	if err := os.MkdirAll(filepath.Dir(publicPath), 0o755); err != nil {
		t.Fatalf("MkdirAll public: %v", err)
	}
	if err := os.WriteFile(publicPath, []byte(`<!DOCTYPE html><html lang="fr"><head><title>Bonjour</title>
<link rel="canonical" href="https://example.test/posts/hello/"></head><body>Bonjour</body></html>`), 0o644); err != nil {
		t.Fatalf("WriteFile public: %v", err)
	}
	contentRoot := t.TempDir()
	for _, lang := range []string{"fr", "en"} {
		path := filepath.Join(contentRoot, "posts", "hello", "index."+lang+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll source: %v", err)
		}
		if err := os.WriteFile(path, []byte("---\ntitle: "+lang+"\n---\nbody\n"), 0o644); err != nil {
			t.Fatalf("WriteFile source: %v", err)
		}
	}

	cfg := config.Default()
	cfg.SiteRoot = publicRoot
	cfg.ContentRoot = contentRoot
	cfg.SiteURL = "https://example.test"
	cfg.DefaultLanguage = "fr"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex: %v", err)
	}
	session, done := newTestClientWithCfg(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	languages := decodeContent(t, res)["languages"].(map[string]any)
	available := languages["available"].([]any)
	if len(available) != 2 || available[0] != "en" || available[1] != "fr" {
		t.Fatalf("languages.available = %#v, want source-observed [en fr] despite FR-only public tree", available)
	}
}

func TestGetCapabilitiesPublishedOutputSchemaIncludesPlanningFields(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClientWithCfg(t, idx, config.Default(), nil)
	defer done()

	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != "get_capabilities" {
			continue
		}
		assertSchemaHasProperties(t, tool, "outputSchema.data.languages", "default", "mode", "available")
		assertSchemaHasProperties(t, tool, "outputSchema.data.features",
			"image_generation_available", "local_hero_generation_available", "external_image_generation_available")
		return
	}
	t.Fatal("get_capabilities missing from tools/list")
}

// findStringLeak reports whether needle appears in any string value anywhere
// in the decoded JSON tree.
func findStringLeak(v any, needle string) bool {
	switch t := v.(type) {
	case string:
		return strings.Contains(t, needle)
	case []any:
		for _, e := range t {
			if findStringLeak(e, needle) {
				return true
			}
		}
	case map[string]any:
		for _, e := range t {
			if findStringLeak(e, needle) {
				return true
			}
		}
	}
	return false
}
