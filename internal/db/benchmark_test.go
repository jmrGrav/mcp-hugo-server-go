package db_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

func openBenchmarkDB(b *testing.B) *db.DB {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.db")
	d, err := db.Open(path)
	if err != nil {
		b.Fatalf("db.Open: %v", err)
	}
	b.Cleanup(func() { _ = d.Close() })
	return d
}

func benchmarkPublicPage(i int) site.Page {
	slug := fmt.Sprintf("/posts/post-%04d/", i)
	return site.Page{
		Slug:       slug,
		Title:      fmt.Sprintf("Post %04d", i),
		Summary:    "Benchmark summary for derived DB sync",
		Tags:       []string{"bench", "mcp", "hugo"},
		Categories: []string{"benchmarks"},
		Date:       "2026-07-12",
		URL:        "https://example.test" + slug,
		Lang:       "en",
	}
}

func benchmarkSourceIndex(b *testing.B, pages int) *hugosite.SourceIndex {
	b.Helper()
	root := b.TempDir()
	for i := 0; i < pages; i++ {
		dir := filepath.Join(root, "posts", fmt.Sprintf("post-%04d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatalf("MkdirAll: %v", err)
		}
		body := fmt.Sprintf("---\ntitle: Post %04d\ntags: [bench, mcp]\ncategories: [benchmarks]\n---\nBody %04d\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(body), 0o644); err != nil {
			b.Fatalf("WriteFile: %v", err)
		}
	}
	idx, err := hugosite.NewSourceIndex(root)
	if err != nil {
		b.Fatalf("NewSourceIndex: %v", err)
	}
	return idx
}

func BenchmarkSyncSourcePage(b *testing.B) {
	for _, size := range []int{100, 1000} {
		srcIdx := benchmarkSourceIndex(b, size)
		pages := srcIdx.ListPages(1, size/2)
		if len(pages) == 0 {
			b.Fatal("no source page for benchmark")
		}
		page := pages[0]
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			d := openBenchmarkDB(b)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p := page
				p.Title = fmt.Sprintf("Post %d-%d", size, i)
				if err := d.SyncSourcePage(p); err != nil {
					b.Fatalf("SyncSourcePage: %v", err)
				}
			}
		})
	}
}

func BenchmarkStartupSync(b *testing.B) {
	for _, size := range []int{100, 1000} {
		srcIdx := benchmarkSourceIndex(b, size)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			root := b.TempDir()
			for i := 0; i < b.N; i++ {
				path := filepath.Join(root, fmt.Sprintf("bench-%d.db", i))
				d, err := db.Open(path)
				if err != nil {
					b.Fatalf("db.Open: %v", err)
				}
				if err := d.StartupSync(nil, srcIdx); err != nil {
					_ = d.Close()
					b.Fatalf("StartupSync: %v", err)
				}
				if err := d.Close(); err != nil {
					b.Fatalf("Close: %v", err)
				}
			}
		})
	}
}

func BenchmarkSearch(b *testing.B) {
	d := openBenchmarkDB(b)
	for i := 0; i < 1000; i++ {
		if err := d.SyncPublicPage(benchmarkPublicPage(i), nil); err != nil {
			b.Fatalf("SyncPublicPage: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := d.Search("Post", 20)
		if err != nil {
			b.Fatalf("Search: %v", err)
		}
		if len(got) == 0 {
			b.Fatal("Search returned no rows")
		}
	}
}
