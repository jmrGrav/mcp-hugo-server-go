package site

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func testConfigWithVerificationSlug(t *testing.T, slug string) config.Config {
	t.Helper()
	return config.Config{TechnicalVerificationSlugs: []string{slug}}
}

// #1186: a third-party domain-ownership-verification file (e.g.
// static/abuseipdb-verification.html) has no title/meta/canonical by
// design and must not be counted as a content-SEO failure — but only once
// the operator has explicitly declared it via config, never inferred from
// the slug/filename itself.

func TestTechnicalVerificationSlugUnclassifiedByDefault(t *testing.T) {
	SetTechnicalVerificationSlugs(nil)
	classifier := NewClassifier(nil)
	p := Page{Slug: "/abuseipdb-verification/"}
	if classifier.IsTechnical(p) {
		t.Fatal("an undeclared slug must not be classified as technical")
	}
	if !classifier.IsContent(p) {
		t.Fatal("an undeclared slug falls through to KindPage, which is content — this is the bug #1186 reports (before the operator opts in)")
	}
}

func TestTechnicalVerificationSlugExcludedFromContentOnceDeclared(t *testing.T) {
	SetTechnicalVerificationSlugs([]string{"abuseipdb-verification"})
	t.Cleanup(func() { SetTechnicalVerificationSlugs(nil) })

	classifier := NewClassifier(nil)
	p := Page{Slug: "/abuseipdb-verification/"}
	if !classifier.IsTechnical(p) {
		t.Fatal("a declared verification slug must classify as KindTechnical")
	}
	if classifier.IsContent(p) {
		t.Fatal("a declared verification slug must be excluded from IsContent (content-SEO population)")
	}
}

func TestTechnicalVerificationSlugDoesNotMatchUnrelatedPages(t *testing.T) {
	SetTechnicalVerificationSlugs([]string{"abuseipdb-verification"})
	t.Cleanup(func() { SetTechnicalVerificationSlugs(nil) })

	classifier := NewClassifier(nil)
	// A legitimate page that merely shares a naming pattern (contains
	// "verification") must never be swept in by a substring/heuristic
	// match — only an exact declared slug qualifies.
	other := Page{Slug: "/blog/email-verification-guide/"}
	if classifier.IsTechnical(other) {
		t.Fatal("an unrelated slug must not match the verification allowlist")
	}
	if !classifier.IsContent(other) {
		t.Fatal("an unrelated slug must remain ordinary content")
	}
}

func TestTechnicalVerificationSlugIsMultiSegmentIgnored(t *testing.T) {
	SetTechnicalVerificationSlugs([]string{"abuseipdb-verification"})
	t.Cleanup(func() { SetTechnicalVerificationSlugs(nil) })

	classifier := NewClassifier(nil)
	// The allowlist only matches single-segment root slugs, mirroring the
	// existing hardcoded technical-route list (robots.txt, security.txt,
	// etc. are all root-level). A nested path sharing the same leaf name
	// must not match.
	nested := Page{Slug: "/posts/abuseipdb-verification/"}
	if classifier.IsTechnical(nested) {
		t.Fatal("a nested slug must not match the root-level verification allowlist")
	}
}

func TestSetTechnicalVerificationSlugsNormalizesInput(t *testing.T) {
	SetTechnicalVerificationSlugs([]string{" /abuseipdb-verification/ ", "", "  "})
	t.Cleanup(func() { SetTechnicalVerificationSlugs(nil) })

	classifier := NewClassifier(nil)
	if !classifier.IsTechnical(Page{Slug: "abuseipdb-verification"}) {
		t.Fatal("SetTechnicalVerificationSlugs must trim whitespace/slashes from declared slugs")
	}
}

func TestNewIndexWiresConfiguredVerificationSlugs(t *testing.T) {
	SetTechnicalVerificationSlugs(nil)
	t.Cleanup(func() { SetTechnicalVerificationSlugs(nil) })

	idx, err := NewIndex(testConfigWithVerificationSlug(t, "abuseipdb-verification"))
	if err != nil {
		t.Fatal(err)
	}
	if !idx.Classifier().IsTechnical(Page{Slug: "abuseipdb-verification"}) {
		t.Fatal("NewIndex must wire config.TechnicalVerificationSlugs into the classifier")
	}
}
