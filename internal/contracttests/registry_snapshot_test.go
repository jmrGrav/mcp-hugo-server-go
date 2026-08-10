package contracttests

// Contract snapshot for the full published tool registry (issue #862).
//
// This is the machine-readable contract snapshot required by AC1/AC4: it
// registers every tool exactly as the write-scoped server does (the superset
// that carries all anonymous + read + write + admin tools), reads back the
// schema each tool *publishes* via tools/list — the shape a real MCP client
// actually sees, not the raw registration struct — and pins the whole thing
// to a checked-in golden file. Any unintended drift in a tool name, required
// scope, description, or input JSON Schema fails `go test ./...`, which is the
// CI gate (AC4).
//
// Regenerating intentionally: after a deliberate tool/schema/description
// change, run
//
//	go test ./internal/contracttests/ -run TestContractToolRegistrySnapshot -update
//
// review the diff to testdata/golden/tool_registry.golden.json, and commit it
// alongside the change. There is no other sanctioned way to move this golden.

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/previewstore"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	toolsadmin "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	toolsanon "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	toolsread "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
	toolswrite "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var updateGolden = flag.Bool("update", false, "regenerate contract golden files instead of comparing")

// contractToolSnapshot is one tool's stable, client-visible contract surface.
type contractToolSnapshot struct {
	Name               string   `json:"name"`
	RequiredScope      string   `json:"required_scope"`
	Description        string   `json:"description"`
	InputSchema        any      `json:"input_schema"`
	OutputSchemaFields []string `json:"output_schema_fields"`
	OutputSchemaSHA256 string   `json:"output_schema_sha256"`
}

// registerFullContractServer wires the complete write-scoped tool surface onto
// one server, mirroring buildWriteScopedServer in internal/server/server.go so
// the snapshot reflects what a real caller reaches, not a test-only subset.
func registerFullContractServer(t *testing.T) *mcp.Server {
	t.Helper()
	idx := mustFixtureIndex(t)
	srcIdx := mustFixtureSourceIndex(t)
	cfg := fixtureConfig()

	pg, err := security.New(cfg.ContentRoot, true)
	if err != nil {
		t.Fatalf("security.New() error = %v", err)
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	toolsanon.Register(s, idx, cfg, srcIdx)
	toolsread.Register(s, idx, cfg, srcIdx)
	toolsread.RegisterWithSourceIndex(s, idx, srcIdx, cfg)
	toolswrite.Register(s, pg, srcIdx, cfg, nil, idx)

	// site.admin — same set and order as buildWriteScopedServer.
	toolsadmin.Register(s, cfg, srcIdx)
	toolsadmin.RegisterVerifyPublication(s, idx, srcIdx, cfg)
	toolsadmin.RegisterPublishChanges(s, idx, srcIdx, cfg)
	previews := previewstore.New()
	toolsadmin.RegisterCreatePreview(s, cfg, previews, "https://preview.example.test")
	toolsadmin.RegisterPreviewAccessTools(s, cfg, previews, "https://preview.example.test")
	toolsadmin.RegisterStorageHealth(s, cfg, srcIdx, previews)
	toolsread.RegisterInspectPreviewRenderedPage(s, idx, srcIdx, cfg, previews, "https://preview.example.test")
	return s
}

// scopeByToolName merges the required-scope authority (Defs) with the published
// schema (tools/list). Scope is only exposed through Defs, so it is joined here.
func scopeByToolName() map[string]string {
	out := make(map[string]string)
	for _, def := range append(append(append(toolsanon.Defs(), toolsread.Defs()...), toolswrite.Defs()...), toolsadmin.Defs()...) {
		out[def.Name] = def.RequiredScope
	}
	return out
}

func outputSchemaSignature(t *testing.T, tool *mcp.Tool) ([]string, string) {
	t.Helper()
	raw, err := json.Marshal(tool.OutputSchema)
	if err != nil {
		t.Fatalf("marshal %s output schema: %v", tool.Name, err)
	}
	sum := sha256.Sum256(raw)
	root, ok := tool.OutputSchema.(map[string]any)
	if !ok {
		t.Fatalf("%s output schema type = %T, want map[string]any", tool.Name, tool.OutputSchema)
	}
	fields := make([]string, 0)
	collectOutputSchemaFields(root, "", &fields)
	sort.Strings(fields)
	return fields, fmt.Sprintf("%x", sum)
}

func collectOutputSchemaFields(schema map[string]any, prefix string, out *[]string) {
	required := map[string]bool{}
	switch values := schema["required"].(type) {
	case []any:
		for _, value := range values {
			if field, ok := value.(string); ok {
				required[field] = true
			}
		}
	case []string:
		for _, field := range values {
			required[field] = true
		}
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, raw := range properties {
		child, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		typeName, _ := child["type"].(string)
		if typeName == "" {
			typeName = "unspecified"
		}
		signature := path + ":" + typeName
		if required[name] {
			signature += ":required"
		}
		*out = append(*out, signature)
		collectOutputSchemaFields(child, path, out)
		if items, ok := child["items"].(map[string]any); ok {
			collectOutputSchemaFields(items, path+"[]", out)
		}
	}
}

func TestContractToolRegistrySnapshot(t *testing.T) {
	// Pin build identity so nothing version-derived leaks into a description.
	restore := setContractBuildInfo(t)
	defer restore()
	buildinfo.Version = "main-testbuild"

	s := registerFullContractServer(t)
	session, done := connectClient(t, s)
	defer done()

	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	scopes := scopeByToolName()
	snapshots := make([]contractToolSnapshot, 0, len(res.Tools))
	for _, tl := range res.Tools {
		scope, ok := scopes[tl.Name]
		if !ok {
			t.Fatalf("tool %q published via tools/list has no Defs() entry — every published tool must declare a required scope", tl.Name)
		}
		fields, hash := outputSchemaSignature(t, tl)
		snapshots = append(snapshots, contractToolSnapshot{
			Name:               tl.Name,
			RequiredScope:      scope,
			Description:        tl.Description,
			InputSchema:        tl.InputSchema,
			OutputSchemaFields: fields,
			OutputSchemaSHA256: hash,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Name < snapshots[j].Name })

	// The snapshot must cover the entire registry (64 tools as of #980), so a
	// tool silently dropped from the published surface fails here, not only in
	// the golden diff.
	wantCount := len(toolsanon.Defs()) + len(toolsread.Defs()) + len(toolswrite.Defs()) + len(toolsadmin.Defs())
	if len(snapshots) != wantCount {
		published := make([]string, 0, len(snapshots))
		for _, sn := range snapshots {
			published = append(published, sn.Name)
		}
		t.Fatalf("published tool count = %d, want %d (registry Defs total)\npublished: %v", len(snapshots), wantCount, published)
	}

	got, err := json.MarshalIndent(snapshots, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent error = %v", err)
	}
	got = append(got, '\n')

	goldenPath := filepath.Join("testdata", "golden", "tool_registry.golden.json")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", goldenPath, err)
		}
		t.Logf("updated %s (%d tools)", goldenPath, len(snapshots))
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v (run with -update to generate)", goldenPath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("tool registry contract drift.\n"+
			"The runtime tool list/schema/description no longer matches the checked-in snapshot.\n"+
			"If this change is intentional, regenerate with:\n"+
			"  go test ./internal/contracttests/ -run TestContractToolRegistrySnapshot -update\n"+
			"and commit the updated %s.\n"+
			"--- got (runtime) ---\n%s\n--- want (golden) ---\n%s", goldenPath, got, want)
	}
}

// TestContractCriticalToolDescriptionsStable pins the descriptions AC1 calls
// out as "critical" — the security- and cost-relevant tools whose wording an
// agent relies on to choose safely — with a targeted substring assertion, so a
// reviewer sees *which* contract broke even before reading the golden diff.
func TestContractCriticalToolDescriptionsStable(t *testing.T) {
	s := registerFullContractServer(t)
	session, done := connectClient(t, s)
	defer done()
	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	byName := make(map[string]*mcp.Tool, len(res.Tools))
	for _, tl := range res.Tools {
		byName[tl.Name] = tl
	}
	for _, tc := range []struct {
		tool         string
		wantContains string
	}{
		{"create_preview", "preview"},
		{"upload_page_asset", "asset"},
		{"delete_page", "delete"},
		{"generate_hero_image", "image"},
		{"check_sri_versions", "supports sha256, sha384, and sha512"},
	} {
		tl, ok := byName[tc.tool]
		if !ok {
			t.Errorf("critical tool %q not published", tc.tool)
			continue
		}
		if tl.Description == "" {
			t.Errorf("critical tool %q has empty description", tc.tool)
		}
		if !strings.Contains(tl.Description, tc.wantContains) {
			t.Errorf("critical tool %q description missing %q: %s", tc.tool, tc.wantContains, tl.Description)
		}
	}
	// Cross-check: every published tool carries a non-empty description, since
	// an agent selects tools by description and a blank one is a silent
	// usability regression the golden alone would not flag as urgent.
	for _, tl := range res.Tools {
		if tl.Description == "" {
			t.Errorf("tool %q has empty description", tl.Name)
		}
	}
	_ = tools.KnownScopes
}

func TestContractToolDescriptionsAvoidLegacyScopesAndPublicSafeWording(t *testing.T) {
	s := registerFullContractServer(t)
	session, done := connectClient(t, s)
	defer done()

	res, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	forbidden := []string{
		"Requires content.read",
		"Requires content.write",
		"Requires site.admin",
		"public-safe",
		"reader-safe",
	}
	for _, tl := range res.Tools {
		for _, bad := range forbidden {
			if strings.Contains(tl.Description, bad) {
				t.Fatalf("tool %q description contains forbidden legacy wording %q: %q", tl.Name, bad, tl.Description)
			}
		}
	}
}
