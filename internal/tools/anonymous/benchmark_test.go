package anonymous_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/anonymous"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mustBenchmarkIndex(b *testing.B) *site.Index {
	b.Helper()
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "public", "minimal")
	cfg := config.Default()
	cfg.SiteRoot = root
	cfg.SiteURL = "https://example.test"
	cfg.SiteName = "example.test"
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	cfg.RejectSymlinks = true
	cfg.RejectHiddenPath = true
	idx, err := site.NewIndex(cfg)
	if err != nil {
		b.Fatalf("NewIndex() error = %v", err)
	}
	return idx
}

func newBenchmarkClient(b *testing.B, idx *site.Index) (*mcp.ClientSession, func()) {
	b.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "bench", Version: "0.1"}, nil)
	anonymous.Register(s, idx, config.Default())

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		b.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "bench-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		b.Fatalf("client connect: %v", err)
	}
	return session, func() { _ = session.Close() }
}

func benchmarkCallTool(b *testing.B, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	b.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		b.Fatalf("CallTool(%q) error = %v", name, err)
	}
	return res
}

func BenchmarkListPages(b *testing.B) {
	idx := mustBenchmarkIndex(b)
	session, done := newBenchmarkClient(b, idx)
	defer done()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := benchmarkCallTool(b, session, "list_pages", map[string]any{"limit": 20, "offset": 0})
		if res.IsError {
			b.Fatalf("list_pages returned error: %#v", res.Content)
		}
	}
}

func BenchmarkGetPage(b *testing.B) {
	idx := mustBenchmarkIndex(b)
	session, done := newBenchmarkClient(b, idx)
	defer done()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := benchmarkCallTool(b, session, "get_page", map[string]any{"slug": "/posts/hello/"})
		if res.IsError {
			b.Fatalf("get_page returned error: %#v", res.Content)
		}
	}
}
