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
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	writepkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestClientWithScope mirrors newTestClientWithCfg but lets the caller
// pick the physical server tier (scopeName), so get_capabilities' effective
// scope/masked-tools fallback (no OAuth in this harness — CtxScope is never
// populated) can be exercised for both the "" (public) and "write" tiers.
func newTestClientWithScope(t *testing.T, idx *site.Index, cfg config.Config, scopeName string) (*mcp.ClientSession, func()) {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	anonymous.Register(s, idx, cfg, scopeName)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	return session, func() { _ = session.Close() }
}

// TestGetCapabilitiesMaskedToolsOmittedWhenAdminScopeFullyUnmasked is an
// envelope-level regression test for the "struct omitempty is a no-op"
// class of bug: MaskedTools must be a pointer so a fully-unmasked
// admin-tier caller gets no "masked_tools" key at all in the JSON, not
// {"masked_tools":{"count":0}}. Admin, not write, is the fully-unmasked
// tier as of v1.8.5 (#1039): a write-tier caller still has the four
// admin-gated managed Hugo binary lifecycle tools masked (see
// TestGetCapabilitiesWriteScopeReportsAdminToolsMasked below).
func TestGetCapabilitiesMaskedToolsOmittedWhenAdminScopeFullyUnmasked(t *testing.T) {
	idx := mustTestIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")

	session, done := newTestClientWithScope(t, idx, cfg, "admin")
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	data := decodeContent(t, res)
	if got := data["effective_scopes"].([]any); len(got) != 1 || got[0] != "admin" {
		t.Fatalf("effective_scopes = %v, want [admin]", got)
	}
	if _, present := data["masked_tools"]; present {
		t.Fatalf("masked_tools = %v, want the key entirely absent when nothing is masked", data["masked_tools"])
	}
}

// TestGetCapabilitiesWriteScopeReportsAdminToolsMasked is a regression test
// for v1.8.5 (#1039): before that change, write implied every tool with no
// exceptions, so a write-tier caller was always fully unmasked. Now the four
// managed Hugo binary lifecycle tools require the separate admin scope, so
// a write-tier caller must see them reported as masked, not silently
// omitted from masked_tools.
func TestGetCapabilitiesWriteScopeReportsAdminToolsMasked(t *testing.T) {
	idx := mustTestIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")

	session, done := newTestClientWithScope(t, idx, cfg, "write")
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	data := decodeContent(t, res)
	if got := data["effective_scopes"].([]any); len(got) != 1 || got[0] != "write" {
		t.Fatalf("effective_scopes = %v, want [write]", got)
	}
	masked, ok := data["masked_tools"].(map[string]any)
	if !ok {
		t.Fatalf("masked_tools = %v, want a present object (admin tools masked for a write-tier caller)", data["masked_tools"])
	}
	if masked["reason"] != "missing_scope" {
		t.Fatalf("masked_tools.reason = %v, want missing_scope", masked["reason"])
	}
	scopes, _ := masked["required_scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "admin" {
		t.Fatalf("masked_tools.required_scopes = %v, want [admin]", masked["required_scopes"])
	}
	if count, _ := masked["count"].(float64); count == 0 {
		t.Fatalf("masked_tools.count = %v, want non-zero (the four admin-gated Hugo lifecycle tools)", masked["count"])
	}
}

// TestGetCapabilitiesMaskedToolsOnPublicTierReportsMissingScope is the
// mirror case: the public ("") tier registers read tools unconditionally
// but never write tools, so it must report effective_scopes:["read"] and a
// non-empty masked_tools with reason missing_scope.
func TestGetCapabilitiesMaskedToolsOnPublicTierReportsMissingScope(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClientWithScope(t, idx, config.Default(), "")
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	data := decodeContent(t, res)
	if got := data["effective_scopes"].([]any); len(got) != 1 || got[0] != "read" {
		t.Fatalf("effective_scopes = %v, want [read]", got)
	}
	masked, ok := data["masked_tools"].(map[string]any)
	if !ok {
		t.Fatalf("masked_tools missing/wrong type: %#v", data["masked_tools"])
	}
	if masked["reason"] != "missing_scope" {
		t.Fatalf("masked_tools.reason = %v, want missing_scope", masked["reason"])
	}
	if count, ok := masked["count"].(float64); !ok || count == 0 {
		t.Fatalf("masked_tools.count = %v, want non-zero", masked["count"])
	}
}

// TestGetCapabilitiesMaskedToolsReportsNotConfiguredWithoutContentRoot is a
// regression test for the "write tier but no content_root" gap: the caller
// reached the write-scoped server (or holds a real write OAuth token) but
// write tools never registered because the deployment has no content_root
// (see newScopedServer's writeEnabled guard). Masking them under
// missing_scope would be actively wrong — the caller already has the scope.
func TestGetCapabilitiesMaskedToolsReportsNotConfiguredWithoutContentRoot(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClientWithScope(t, idx, config.Default(), "write")
	defer done()

	res := callTool(t, session, "get_capabilities", map[string]any{})
	if res.IsError {
		t.Fatalf("get_capabilities failed: %#v", res)
	}
	data := decodeContent(t, res)
	masked, ok := data["masked_tools"].(map[string]any)
	if !ok {
		t.Fatalf("masked_tools missing/wrong type: %#v", data["masked_tools"])
	}
	if masked["reason"] != "not_configured" {
		t.Fatalf("masked_tools.reason = %v, want not_configured (write scope granted, but no content_root)", masked["reason"])
	}
}

// TestGetCapabilitiesReportsLimitsAndFeatureFlags is the #859 contract test:
// get_capabilities must expose the hard limits (from the write tools' own
// TestGetCapabilitiesToolCatalogCountsGrowWithScope is a regression test
// for #1175: tool_catalog.visible_count must strictly increase as scope
// widens (public < write < admin, with content_root configured so write/
// admin tools actually register), and tool_names_revision must be a
// deterministic, differently-valued hash at each scope — the whole point
// of the field is that a client comparing its own loaded tool count/hash
// against this can detect a stale discovery snapshot.
func TestGetCapabilitiesToolCatalogCountsGrowWithScope(t *testing.T) {
	idx := mustTestIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")

	toolCatalog := func(scopeName string) (count int, revision string) {
		session, done := newTestClientWithScope(t, idx, cfg, scopeName)
		defer done()
		res := callTool(t, session, "get_capabilities", map[string]any{})
		if res.IsError {
			t.Fatalf("get_capabilities(%q) failed: %#v", scopeName, res)
		}
		data := decodeContent(t, res)
		tc, ok := data["tool_catalog"].(map[string]any)
		if !ok {
			t.Fatalf("get_capabilities(%q): tool_catalog missing/wrong type: %#v", scopeName, data["tool_catalog"])
		}
		c, _ := tc["visible_count"].(float64)
		rev, _ := tc["tool_names_revision"].(string)
		return int(c), rev
	}

	publicCount, publicRev := toolCatalog("")
	writeCount, writeRev := toolCatalog("write")
	adminCount, adminRev := toolCatalog("admin")

	if publicCount == 0 {
		t.Fatal("public tier visible_count = 0, want > 0")
	}
	if writeCount <= publicCount {
		t.Fatalf("write visible_count = %d, want > public visible_count %d", writeCount, publicCount)
	}
	if adminCount <= writeCount {
		t.Fatalf("admin visible_count = %d, want > write visible_count %d (the 4 admin-gated Hugo lifecycle tools)", adminCount, writeCount)
	}
	// The admin-write gap is exactly the 4 managed Hugo binary lifecycle
	// tools masked_tools already names for a write-tier caller.
	if got := adminCount - writeCount; got != 4 {
		t.Fatalf("admin visible_count - write visible_count = %d, want 4", got)
	}

	for _, rev := range []string{publicRev, writeRev, adminRev} {
		if !strings.HasPrefix(rev, "sha256:") || len(rev) <= len("sha256:") {
			t.Fatalf("tool_names_revision = %q, want a non-empty sha256:... value", rev)
		}
	}
	if publicRev == writeRev || writeRev == adminRev || publicRev == adminRev {
		t.Fatalf("tool_names_revision must differ across scopes with different tool sets: public=%q write=%q admin=%q", publicRev, writeRev, adminRev)
	}

	// Determinism: calling again at the same scope must return the same
	// count/hash (no non-determinism from, e.g., map iteration order).
	repeatCount, repeatRev := toolCatalog("write")
	if repeatCount != writeCount || repeatRev != writeRev {
		t.Fatalf("tool_catalog not deterministic across calls: first (%d, %q), second (%d, %q)", writeCount, writeRev, repeatCount, repeatRev)
	}
}

// TestGetCapabilitiesToolCatalogWithoutContentRootMatchesReadScope is the
// "write tier but no content_root" case (mirrors
// TestGetCapabilitiesMaskedToolsReportsNotConfiguredWithoutContentRoot):
// write/admin tools never registered, so tool_catalog must report the same
// visible_count/tool_names_revision a plain read-scope caller sees, not an
// inflated count for tools that don't actually exist on this deployment.
func TestGetCapabilitiesToolCatalogWithoutContentRootMatchesReadScope(t *testing.T) {
	idx := mustTestIndex(t)

	readSession, readDone := newTestClientWithScope(t, idx, config.Default(), "")
	defer readDone()
	readRes := callTool(t, readSession, "get_capabilities", map[string]any{})
	readTC := decodeContent(t, readRes)["tool_catalog"].(map[string]any)

	writeSession, writeDone := newTestClientWithScope(t, idx, config.Default(), "write")
	defer writeDone()
	writeRes := callTool(t, writeSession, "get_capabilities", map[string]any{})
	writeTC := decodeContent(t, writeRes)["tool_catalog"].(map[string]any)

	if readTC["visible_count"] != writeTC["visible_count"] {
		t.Fatalf("visible_count without content_root: read=%v write=%v, want equal", readTC["visible_count"], writeTC["visible_count"])
	}
	if readTC["tool_names_revision"] != writeTC["tool_names_revision"] {
		t.Fatalf("tool_names_revision without content_root: read=%v write=%v, want equal", readTC["tool_names_revision"], writeTC["tool_names_revision"])
	}
}

// TestGetCapabilitiesToolRegistryDigestWellFormedAndStable is #1225's
// baseline: get_capabilities computes tool_registry_digest live from this
// session's own *mcp.Server (toolRegistryDigestForServer), so it is always
// present — no separate publish step to omit — and, since it is cached
// per-server via a closure-local sync.Once, two calls on the SAME session
// must return the identical value (proves the cache, not just that a
// digest happens to compute the same thing twice).
func TestGetCapabilitiesToolRegistryDigestWellFormedAndStable(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClientWithScope(t, idx, config.Default(), "")
	defer done()

	digest := func() string {
		res := callTool(t, session, "get_capabilities", map[string]any{})
		if res.IsError {
			t.Fatalf("get_capabilities failed: %#v", res)
		}
		tc := decodeContent(t, res)["tool_catalog"].(map[string]any)
		d, _ := tc["tool_registry_digest"].(string)
		return d
	}

	first := digest()
	if !strings.HasPrefix(first, "sha256:") || len(first) <= len("sha256:") {
		t.Fatalf("tool_registry_digest = %q, want a non-empty sha256:... value", first)
	}
	if second := digest(); second != first {
		t.Fatalf("tool_registry_digest changed across calls on the same session: %q then %q", first, second)
	}
}

// TestGetCapabilitiesToolRegistryDigestReflectsThisServersActualTools is the
// #1225 redesign's core property (fixed after the original admin-superset
// design was found to hide exposure-profile drift, see
// docs/mcp-contract.md §6.28): the digest is computed from THIS session's
// own *mcp.Server, not a separately-reconstructed registry list — so a
// server carrying one extra registered tool gets a different digest than
// an otherwise-identical server without it. The genuinely-different-scope
// case (public vs write, with real write/admin tools registered) is
// covered end-to-end in internal/server (TestToolRegistryDigestDiffersByScope,
// TestToolRegistryDigestDiffersByExposureProfile) — this package's own test
// helpers only ever register anonymous.Register's tools regardless of the
// scopeName string passed to newTestClientWithScope, so scope alone can't
// exercise this property here.
func TestGetCapabilitiesToolRegistryDigestReflectsThisServersActualTools(t *testing.T) {
	idx := mustTestIndex(t)

	baseSession, baseDone := newTestClientWithScope(t, idx, config.Default(), "")
	defer baseDone()
	baseRes := callTool(t, baseSession, "get_capabilities", map[string]any{})
	baseTC := decodeContent(t, baseRes)["tool_catalog"].(map[string]any)
	baseDigest, _ := baseTC["tool_registry_digest"].(string)

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	anonymous.Register(s, idx, config.Default(), "")
	mcp.AddTool(s, &mcp.Tool{Name: "extra_test_only_tool", Description: "not a real product tool, exists only to change this server's tool set"},
		func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, struct{ X string }, error) {
			return nil, struct{ X string }{"ok"}, nil
		})
	ctx := context.Background()
	et1, et2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, et1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	extraClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	extraSession, err := extraClient.Connect(ctx, et2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = extraSession.Close() }()
	extraRes := callTool(t, extraSession, "get_capabilities", map[string]any{})
	extraTC := decodeContent(t, extraRes)["tool_catalog"].(map[string]any)
	extraDigest, _ := extraTC["tool_registry_digest"].(string)

	if baseDigest == "" || extraDigest == "" {
		t.Fatalf("tool_registry_digest is empty: base=%q extra=%q", baseDigest, extraDigest)
	}
	if baseDigest == extraDigest {
		t.Fatalf("tool_registry_digest unchanged after registering an extra tool on this exact *mcp.Server: both %q", baseDigest)
	}
}

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
	if got := decodeEnvelope(t, res)["meta"].(map[string]any)["content_provenance"]; got != "server_generated_trusted" {
		t.Fatalf("get_capabilities provenance = %v, want server_generated_trusted", got)
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
	assetUpload, ok := limits["asset_upload"].(map[string]any)
	if !ok {
		t.Fatalf("limits.asset_upload missing/wrong type: %#v", limits["asset_upload"])
	}
	if got, want := int(assetUpload["max_asset_bytes"].(float64)), writepkg.AssetMaxBytes(); got != want {
		t.Errorf("asset_upload.max_asset_bytes = %d, want %d", got, want)
	}
	// cfg.MaxRequestBytes is unset here (config.Default() sets 1 MiB), so
	// the recommendation must land strictly below both max_request_bytes
	// and asset_max_bytes — the exact contract gap #1190 reports.
	recommended := int(assetUpload["recommended_inline_max_bytes"].(float64))
	if recommended <= 0 || int64(recommended) >= cfg.MaxRequestBytes {
		t.Errorf("asset_upload.recommended_inline_max_bytes = %d, want > 0 and < max_request_bytes (%d)", recommended, cfg.MaxRequestBytes)
	}
	if recommended >= writepkg.AssetMaxBytes() {
		t.Errorf("asset_upload.recommended_inline_max_bytes = %d, want < asset_max_bytes (%d)", recommended, writepkg.AssetMaxBytes())
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
	disabled, ok := data["disabled_features"].([]any)
	if !ok {
		t.Fatalf("disabled_features missing/wrong type: %#v", data["disabled_features"])
	}
	seenCloudflare := false
	seenExternalImage := false
	seenDurablePersistence := false
	for _, raw := range disabled {
		entry, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("disabled_features entry type = %T", raw)
		}
		if entry["reason"] != "feature_disabled" {
			t.Errorf("disabled feature reason = %v, want feature_disabled", entry["reason"])
		}
		if entry["name"] == "cloudflare_purge" {
			seenCloudflare = true
		}
		if entry["name"] == "external_image_generation" {
			seenExternalImage = true
		}
		if entry["name"] == "durable_persistence" {
			seenDurablePersistence = true
			if entry["required_configuration"] != "db_path" {
				t.Errorf("durable_persistence required_configuration = %v, want db_path", entry["required_configuration"])
			}
		}
	}
	if !seenCloudflare {
		t.Error("disabled_features missing cloudflare_purge")
	}
	if seenExternalImage {
		t.Error("disabled_features reported external_image_generation despite ImageGenURL being configured")
	}
	if !seenDurablePersistence {
		t.Error("disabled_features missing durable_persistence when db_path is unset — this is the discoverability fix for the v1.8.6 deploy pitfall (#1099 follow-up)")
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
