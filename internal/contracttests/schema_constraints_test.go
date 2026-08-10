package contracttests

import (
	"context"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	toolsadmin "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestContractResponseModeVocabularyIsPublishedForEveryTool keeps discovery
// and handler validation aligned without reintroducing a JSON Schema enum.
// It deliberately scans the complete write-scoped registry rather than a
// representative sample: a new tool must not silently publish response_mode
// as an undocumented free string (#997).
func TestContractResponseModeVocabularyIsPublishedForEveryTool(t *testing.T) {
	s := registerFullContractServer(t)
	session, done := connectClient(t, s)
	defer done()

	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	found := 0
	for _, tl := range res.Tools {
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: input schema type = %T, want map[string]any", tl.Name, tl.InputSchema)
		}
		properties, _ := schema["properties"].(map[string]any)
		property, hasResponseMode := properties["response_mode"].(map[string]any)
		if !hasResponseMode {
			continue
		}
		found++
		description, _ := property["description"].(string)
		for _, want := range []string{"standard", "compact", "default"} {
			if !strings.Contains(strings.ToLower(description), want) {
				t.Errorf("%s.response_mode description = %q, missing %q", tl.Name, description, want)
			}
		}
		if _, hasEnum := property["enum"]; hasEnum {
			t.Errorf("%s.response_mode must remain handler-validated, not a JSON Schema enum", tl.Name)
		}
	}
	if found == 0 {
		t.Fatal("full tool registry contains no response_mode properties")
	}
}

// toolInputSchemaProperty fetches tool's published input schema via
// tools/list and returns the JSON object for its named property, failing
// the test if the tool or property isn't found.
func toolInputSchemaProperty(t *testing.T, session *mcp.ClientSession, tool, field string) map[string]any {
	t.Helper()
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	for _, tl := range res.Tools {
		if tl.Name != tool {
			continue
		}
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Fatalf("%s: InputSchema type = %T, want map[string]any", tool, tl.InputSchema)
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s: schema has no properties object", tool)
		}
		prop, ok := props[field].(map[string]any)
		if !ok {
			t.Fatalf("%s: schema has no property %q", tool, field)
		}
		return prop
	}
	t.Fatalf("tool %q not found in tools/list", tool)
	return nil
}

// TestContractInvalidEnumInputsReturnStructuredError is the #892 anti-drift
// guard, and the most important piece of that fix.
//
// Fields that accept only a fixed set of string values (response_mode across
// most tools, search_pages.match) are validated in the handler / WrapTool
// layer and are deliberately NOT published as a JSON-Schema enum. The reason
// is the exact bug #892 fixes: the go-sdk validates arguments against the
// published schema (server.go, toolForErr → applySchema) *before* our handler
// runs, and on failure calls CallToolResult.SetError, which populates only
// flat Content and IsError — never StructuredContent. So a published enum
// makes an out-of-enum value bypass our entire toolcontract pipeline and
// return a bare text error with no machine-readable code/resolution.
//
// This test drives a known-invalid value at every such field and asserts the
// contract that matters to an agent: the result is a *structured* error —
// non-nil StructuredContent carrying a recognized error code — not merely
// IsError with a text blob. It also guards against regression by asserting the
// field is not (re-)published as a schema enum, since doing so would silently
// reintroduce the schema-layer bypass.
//
// Fail-red/pass-green: against the pre-#892 code (WithEnum published on these
// fields) the schema layer rejects the value and StructuredContent is nil, so
// every case fails; after the migration to handler/WrapTool validation every
// case produces the structured invalid_params envelope and passes.
func TestContractInvalidEnumInputsReturnStructuredError(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()
	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	tests := []struct {
		session *mcp.ClientSession
		tool    string
		field   string
		args    map[string]any
	}{
		// response_mode across a representative spread of anonymous- and
		// read-scoped tools (param-free where possible so the invalid enum
		// value is the sole reason for rejection).
		{anonSession, "list_pages", "response_mode", map[string]any{"response_mode": "ultra_compact"}},
		{anonSession, "get_recent_posts", "response_mode", map[string]any{"response_mode": "verbose"}},
		{anonSession, "list_tags", "response_mode", map[string]any{"response_mode": "full"}},
		{anonSession, "list_categories", "response_mode", map[string]any{"response_mode": "ids_only"}},
		{anonSession, "get_sitemap", "response_mode", map[string]any{"response_mode": "tiny"}},
		{anonSession, "get_feed", "response_mode", map[string]any{"response_mode": "nope"}},
		{anonSession, "get_site_information", "response_mode", map[string]any{"response_mode": "COMPACT"}},
		{anonSession, "search_pages", "response_mode", map[string]any{"query": "hello", "response_mode": "ultra"}},
		{readSession, "get_site_health", "response_mode", map[string]any{"response_mode": "brief"}},
		{readSession, "validate_site", "response_mode", map[string]any{"response_mode": "loud"}},
		{readSession, "get_broken_links", "response_mode", map[string]any{"response_mode": "xxx"}},
		{readSession, "list_content_types", "response_mode", map[string]any{"response_mode": "standardish"}},
		{readSession, "search_content", "response_mode", map[string]any{"query": "hello", "response_mode": "verbose"}},
		// search_pages.match — the other schema-only enum migrated in #892.
		{anonSession, "search_pages", "match", map[string]any{"query": "hello", "match": "fuzzy"}},
	}

	for _, tc := range tests {
		t.Run(tc.tool+"."+tc.field, func(t *testing.T) {
			// Regression guard: the field must not carry a published enum,
			// or the schema layer would reject before our pipeline runs.
			schema := toolInputSchemaProperty(t, tc.session, tc.tool, tc.field)
			if _, hasEnum := schema["enum"]; hasEnum {
				t.Fatalf("%s.%s: field re-published as JSON-Schema enum; out-of-enum values "+
					"would be rejected by the SDK before the handler and lose StructuredContent (#892)",
					tc.tool, tc.field)
			}

			res := callTool(t, tc.session, tc.tool, tc.args)
			if !res.IsError {
				t.Fatalf("%s: invalid %s=%v did not produce an error result", tc.tool, tc.field, tc.args[tc.field])
			}
			// The crux of #892: a schema-layer rejection leaves this nil.
			if res.StructuredContent == nil {
				t.Fatalf("%s: invalid %s=%v produced IsError but nil StructuredContent — "+
					"error bypassed the toolcontract pipeline (#892)", tc.tool, tc.field, tc.args[tc.field])
			}
			m := decodeContent(t, res)
			assertToolErrorEnvelope(t, tc.tool, m, "invalid_params")
		})
	}
}

// TestContractEnumFieldsStillAcceptValidValues preserves the runtime
// acceptance half of #418's coverage after the published enums were removed
// in #892: the migrated fields must still accept every value they document.
func TestContractEnumFieldsStillAcceptValidValues(t *testing.T) {
	restoreBuildInfo := setContractBuildInfo(t)
	defer restoreBuildInfo()

	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()

	tests := []struct {
		tool string
		args map[string]any
	}{
		{"list_pages", map[string]any{"response_mode": "standard"}},
		{"list_pages", map[string]any{"response_mode": "compact"}},
		{"list_pages", map[string]any{}}, // omitted == standard
		{"search_pages", map[string]any{"query": "hello", "match": "any"}},
		{"search_pages", map[string]any{"query": "hello", "match": "title_exact"}},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			res := callTool(t, anonSession, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("%s with valid args %v returned error: %s", tc.tool, tc.args, marshalAny(t, res.Content))
			}
		})
	}
}

func TestContractRunPostBuildHooksPublishesDryRunBoolean(t *testing.T) {
	cfg := fixtureConfig()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	toolsadmin.RegisterHooks(s, cfg)
	session, done := connectClient(t, s)
	defer done()

	prop := toolInputSchemaProperty(t, session, "run_post_build_hooks", "dry_run")
	if got := asString(prop["type"]); got != "boolean" {
		t.Fatalf("run_post_build_hooks.dry_run schema type = %q, want boolean", got)
	}
}

func TestContractCompactModeTrimsMetaOnReadTools(t *testing.T) {
	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()
	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	tests := []struct {
		name    string
		session *mcp.ClientSession
		tool    string
		args    map[string]any
	}{
		{
			name:    "anonymous.list_pages",
			session: anonSession,
			tool:    "list_pages",
			args:    map[string]any{"limit": 2, "offset": 0, "response_mode": "compact"},
		},
		{
			name:    "anonymous.get_page",
			session: anonSession,
			tool:    "get_page",
			args:    map[string]any{"slug": "/posts/hello/", "response_mode": "compact"},
		},
		{
			name:    "anonymous.search_pages",
			session: anonSession,
			tool:    "search_pages",
			args:    map[string]any{"query": "hello", "response_mode": "compact"},
		},
		{
			name:    "read.get_page_markdown",
			session: readSession,
			tool:    "get_page_markdown",
			args:    map[string]any{"slug": "/posts/hello/", "response_mode": "compact"},
		},
		{
			name:    "read.search_content",
			session: readSession,
			tool:    "search_content",
			args:    map[string]any{"type": "all", "limit": 2, "offset": 0, "response_mode": "compact"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := callTool(t, tc.session, tc.tool, tc.args)
			if res.IsError {
				t.Fatalf("%s returned error: %s", tc.tool, marshalAny(t, res.Content))
			}
			m := decodeContent(t, res)
			meta, ok := m["meta"].(map[string]any)
			if !ok {
				t.Fatalf("%s meta type = %T, want map[string]any", tc.tool, m["meta"])
			}
			if got := asString(meta["schema_version"]); got != toolcontract.ToolResultVersion {
				t.Fatalf("%s compact meta.schema_version = %q, want %q", tc.tool, got, toolcontract.ToolResultVersion)
			}
			// #567: three independent live audits flagged compact mode
			// dropping release_version/commit/build_channel as confusing —
			// an agent in compact mode couldn't tell which server build
			// answered it. These are cheap, static per-process values, not
			// per-request payload weight, so compact mode now only narrows
			// data/row-level payload, never meta's release-identity fields.
			// release_version is always non-empty (toolcontract.NewMeta).
			// commit/build_channel use `omitempty` and are legitimately
			// absent for an untagged dev/test build (same caveat as
			// TestContractSearchPagesDefaultModeKeepsFullMeta below), so
			// their presence isn't asserted directly here — instead, compare
			// against the same call's standard-mode meta, which must be
			// identical apart from generated_at (the one field compact mode
			// still intentionally trims).
			if got := asString(meta["release_version"]); got == "" {
				t.Fatalf("%s compact meta.release_version = empty, want populated", tc.tool)
			}
			standardArgs := map[string]any{}
			for k, v := range tc.args {
				if k != "response_mode" {
					standardArgs[k] = v
				}
			}
			standardRes := callTool(t, tc.session, tc.tool, standardArgs)
			if standardRes.IsError {
				t.Fatalf("%s (standard mode) returned error: %s", tc.tool, marshalAny(t, standardRes.Content))
			}
			standardMeta, ok := decodeContent(t, standardRes)["meta"].(map[string]any)
			if !ok {
				t.Fatalf("%s standard-mode meta type = %T, want map[string]any", tc.tool, standardMeta)
			}
			delete(standardMeta, "generated_at")
			if len(meta) != len(standardMeta) {
				t.Fatalf("%s compact meta = %v, want same keys as standard meta minus generated_at = %v", tc.tool, meta, standardMeta)
			}
			for k, v := range standardMeta {
				if meta[k] != v {
					t.Fatalf("%s compact meta[%q] = %v, want %v (same as standard mode)", tc.tool, k, meta[k], v)
				}
			}
			// meta.generated_at remains the one intentionally-trimmed field:
			// the root-level generated_at compatibility field below already
			// carries that value in compact mode.
			if _, ok := meta["generated_at"]; ok {
				t.Fatalf("%s compact meta unexpectedly contains \"generated_at\": %v", tc.tool, meta["generated_at"])
			}
			if got := asString(m["generated_at"]); got == "" {
				t.Fatalf("%s root generated_at = empty, want preserved root timestamp", tc.tool)
			}
		})
	}
}

// TestContractSearchPagesDefaultModeKeepsFullMeta covers #553: a live
// v1.5.4 audit reported search_pages's meta as unexpectedly incomplete
// (only schema_version) on a call made without response_mode. This proves
// the default (standard) mode always carries the full meta object —
// confirming that finding was compact mode (#526) being invoked, not a
// default-mode regression, since search_pages uses the same
// NewMeta/WrapTool pipeline as every other tool with no bespoke path that
// could independently produce a trimmed meta.
func TestContractSearchPagesDefaultModeKeepsFullMeta(t *testing.T) {
	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()

	res := callTool(t, anonSession, "search_pages", map[string]any{"query": "hello"})
	if res.IsError {
		t.Fatalf("search_pages returned error: %s", marshalAny(t, res.Content))
	}
	m := decodeContent(t, res)
	meta, ok := m["meta"].(map[string]any)
	if !ok {
		t.Fatalf("search_pages meta type = %T, want map[string]any", m["meta"])
	}
	// generated_at/release_version are always non-empty (see toolcontract.NewMeta);
	// commit/build_channel are legitimately omitted for an untagged dev/test
	// build (omitempty), so they aren't asserted here. The point is proving
	// this is NOT the compact-trimmed shape (schema_version
	// only), not that every optional identity field is populated.
	for _, field := range []string{"generated_at", "release_version", "schema_version"} {
		if got := asString(meta[field]); got == "" {
			t.Fatalf("search_pages default-mode meta.%s = empty, want populated (#553)", field)
		}
	}
	if len(meta) <= 1 {
		t.Fatalf("search_pages default-mode meta = %v, want more than just schema_version (that shape is compact-mode-only, #526)", meta)
	}
}

// TestContractPublishedLimitMaximumMatchesRuntimeClamp covers #418: the
// schema's published `maximum` for a paginated tool's `limit` must match
// the value that tool's runtime clampLimit call actually enforces, and a
// request one past that maximum must actually be rejected at the schema
// layer (not just documented as rejected).
func TestContractPublishedLimitMaximumMatchesRuntimeClamp(t *testing.T) {
	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()

	tests := []struct {
		tool string
		max  float64
		args map[string]any
	}{
		{"list_pages", 50, map[string]any{}},
		{"search_pages", 50, map[string]any{"query": "hello"}},
		{"get_recent_posts", 50, map[string]any{}},
		{"get_sitemap", 200, map[string]any{}},
		{"get_feed", 50, map[string]any{}},
	}

	for _, tc := range tests {
		t.Run(tc.tool, func(t *testing.T) {
			schema := toolInputSchemaProperty(t, anonSession, tc.tool, "limit")
			maxRaw, ok := schema["maximum"]
			if !ok {
				t.Fatalf("%s.limit: schema has no published maximum, want %v", tc.tool, tc.max)
			}
			got, ok := maxRaw.(float64)
			if !ok || got != tc.max {
				t.Fatalf("%s.limit: published maximum = %v, want %v", tc.tool, maxRaw, tc.max)
			}
			args := make(map[string]any, len(tc.args)+1)
			for k, v := range tc.args {
				args[k] = v
			}
			args["limit"] = int(tc.max) + 1
			res := callTool(t, anonSession, tc.tool, args)
			if !res.IsError {
				t.Fatalf("%s limit=%d (published maximum + 1): expected schema-level rejection, got success", tc.tool, int(tc.max)+1)
			}

			if _, ok := schema["minimum"]; ok {
				t.Fatalf("%s.limit: schema publishes a minimum, but runtime clampLimit treats limit<=0 as \"use default\" — a minimum would reject a value the server accepts", tc.tool)
			}
			zeroArgs := make(map[string]any, len(tc.args)+1)
			for k, v := range tc.args {
				zeroArgs[k] = v
			}
			zeroArgs["limit"] = 0
			res = callTool(t, anonSession, tc.tool, zeroArgs)
			if res.IsError {
				t.Fatalf("%s limit=0: expected success (runtime treats 0 as \"use default\"), got schema-level rejection: %v", tc.tool, res.Content)
			}
		})
	}
}
