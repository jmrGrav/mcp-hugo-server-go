package contracttests

import (
	"context"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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

func stringSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, s := range a {
		counts[s]++
	}
	for _, s := range b {
		counts[s]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// TestContractPublishedEnumsMatchRuntimeAcceptedValues covers #418: for each
// tool/field pair known to accept only a fixed set of string values, the
// schema published via tools/list must carry the exact same enum the
// handler runtime accepts — not a superset (which would let a well-behaved
// client send a value the server actually rejects) and not a subset (which
// would block a value the server accepts). A drift here means the schema
// and the runtime silently disagree, defeating the whole point of
// publishing the constraint.
func TestContractPublishedEnumsMatchRuntimeAcceptedValues(t *testing.T) {
	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	anonSession, anonDone := newAnonymousSession(t, idx, cfg, srcIdx)
	defer anonDone()
	readSession, readDone := newReadSession(t, idx, cfg, srcIdx)
	defer readDone()

	tests := []struct {
		session  *mcp.ClientSession
		tool     string
		field    string
		wantEnum []string
	}{
		{anonSession, "list_pages", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "get_page", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "search_pages", "match", []string{"", "any", "title_exact"}},
		{anonSession, "search_pages", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "get_recent_posts", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "list_tags", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "list_categories", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "get_sitemap", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "get_feed", "response_mode", []string{"", "standard", "compact"}},
		{anonSession, "get_site_information", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "get_page_markdown", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "get_page_frontmatter", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "get_related_content", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "build_agent_context", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "export_agent_context", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "get_page_for_edit", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "search_content", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "explain_structure", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "get_site_health", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "validate_frontmatter", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "validate_site", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "get_broken_links", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "get_backlinks", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "suggest_links", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "inspect_rendered", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "list_content_types", "response_mode", []string{"", "standard", "compact"}},
		{readSession, "list_page_assets", "response_mode", []string{"", "standard", "compact"}},
	}

	for _, tc := range tests {
		t.Run(tc.tool+"."+tc.field, func(t *testing.T) {
			schema := toolInputSchemaProperty(t, tc.session, tc.tool, tc.field)
			enumRaw, ok := schema["enum"].([]any)
			if !ok {
				t.Fatalf("%s.%s: schema has no published enum, want %v", tc.tool, tc.field, tc.wantEnum)
			}
			got := make([]string, len(enumRaw))
			for i, v := range enumRaw {
				got[i] = v.(string)
			}
			if !stringSetEqual(got, tc.wantEnum) {
				t.Fatalf("%s.%s: published enum = %v, want %v", tc.tool, tc.field, got, tc.wantEnum)
			}
		})
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
			for _, forbidden := range []string{"generated_at", "server_version", "release_version", "commit", "build_channel"} {
				if _, ok := meta[forbidden]; ok {
					t.Fatalf("%s compact meta unexpectedly contains %q: %v", tc.tool, forbidden, meta[forbidden])
				}
			}
			if got := asString(m["generated_at"]); got == "" {
				t.Fatalf("%s root generated_at = empty, want preserved root timestamp", tc.tool)
			}
		})
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
