package site

import (
	"fmt"
	"strings"
	"testing"
)

func benchmarkIndex(size int) *Index {
	entries := make([]entry, 0, size)
	bySlug := make(map[string]int, size)
	for i := 0; i < size; i++ {
		slug := fmt.Sprintf("/posts/post-%04d/", i)
		p := Page{
			Slug:       slug,
			Title:      fmt.Sprintf("Post %04d", i),
			Summary:    "Security hardening and Hugo MCP benchmark content",
			Tags:       []string{"security", "hugo", "mcp"},
			Categories: []string{"benchmarks"},
			Date:       fmt.Sprintf("2026-07-%02d", (i%28)+1),
			URL:        "https://example.test" + slug,
			Lang:       "en",
		}
		if i%7 == 0 {
			p.Summary = "Performance notes about search_content and build_site"
		}
		if i%13 == 0 {
			p.Tags = append(p.Tags, "search")
		}
		entries = append(entries, entry{page: p})
		bySlug[p.Slug] = i
	}
	return &Index{
		entries: entries,
		bySlug:  bySlug,
	}
}

func BenchmarkIndexSearch(b *testing.B) {
	for _, size := range []int{100, 1000, 5000} {
		idx := benchmarkIndex(size)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := idx.Search("security", 20)
				if len(got) == 0 {
					b.Fatal("Search returned no results")
				}
			}
		})
	}
}

func BenchmarkIndexGetBySlug(b *testing.B) {
	for _, size := range []int{100, 1000, 5000} {
		idx := benchmarkIndex(size)
		target := fmt.Sprintf("/posts/post-%04d/", size/2)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				p, ok := idx.GetBySlug(target)
				if !ok || !strings.Contains(p.Title, "Post") {
					b.Fatalf("GetBySlug(%q) failed", target)
				}
			}
		})
	}
}

func BenchmarkIndexSitemap(b *testing.B) {
	for _, size := range []int{100, 1000, 5000} {
		idx := benchmarkIndex(size)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := idx.Sitemap()
				if len(got) != size {
					b.Fatalf("Sitemap len=%d want %d", len(got), size)
				}
			}
		})
	}
}

func BenchmarkIndexGetFeed(b *testing.B) {
	for _, size := range []int{100, 1000, 5000} {
		idx := benchmarkIndex(size)
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got := idx.GetFeed(25)
				if len(got) == 0 {
					b.Fatal("GetFeed returned no items")
				}
			}
		})
	}
}
