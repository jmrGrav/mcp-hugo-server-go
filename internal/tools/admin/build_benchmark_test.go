package admin

import (
	"strings"
	"testing"
)

func BenchmarkBuildOutputSummary(b *testing.B) {
	stderr := []byte(strings.Repeat("/very/secret/site/root/content/post.md: Error: template failed to render\n", 20))
	stdout := []byte(strings.Repeat("WARN render fallback\n", 20))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got := buildOutputSummary(stderr, stdout, "/very/secret/site/root", "/very/secret/site/root/public")
		if got == "" {
			b.Fatal("buildOutputSummary returned empty string")
		}
	}
}
