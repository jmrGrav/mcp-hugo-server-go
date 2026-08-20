package site

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

// #1186: a third-party domain-ownership-verification file (e.g.
// static/abuseipdb-verification.html) has no title/meta/canonical by
// design and must not be counted as a content-SEO failure — but only once
// the operator has explicitly declared it via config, never inferred from
// the slug/filename itself. The allowlist lives per-classifier (sourced
// from *Index.verificationSlugs, captured once at NewIndex(cfg) time), not
// as shared package state, so two indexes built from different configs
// never clobber each other's allowlist.

func classifierWithVerificationSlugs(slugs ...string) *ContentClassifier {
	return newClassifierFromPages(nil, normalizeVerificationSlugs(slugs))
}

func TestTechnicalVerificationSlugUnclassifiedByDefault(t *testing.T) {
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
	classifier := classifierWithVerificationSlugs("abuseipdb-verification")
	p := Page{Slug: "/abuseipdb-verification/"}
	if !classifier.IsTechnical(p) {
		t.Fatal("a declared verification slug must classify as KindTechnical")
	}
	if classifier.IsContent(p) {
		t.Fatal("a declared verification slug must be excluded from IsContent (content-SEO population)")
	}
}

func TestTechnicalVerificationSlugDoesNotMatchUnrelatedPages(t *testing.T) {
	classifier := classifierWithVerificationSlugs("abuseipdb-verification")
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
	classifier := classifierWithVerificationSlugs("abuseipdb-verification")
	// The allowlist only matches single-segment root slugs, mirroring the
	// existing hardcoded technical-route list (robots.txt, security.txt,
	// etc. are all root-level). A nested path sharing the same leaf name
	// must not match.
	nested := Page{Slug: "/posts/abuseipdb-verification/"}
	if classifier.IsTechnical(nested) {
		t.Fatal("a nested slug must not match the root-level verification allowlist")
	}
}

func TestNormalizeVerificationSlugsTrimsInput(t *testing.T) {
	classifier := classifierWithVerificationSlugs(" /abuseipdb-verification/ ", "", "  ")
	if !classifier.IsTechnical(Page{Slug: "abuseipdb-verification"}) {
		t.Fatal("normalizeVerificationSlugs must trim whitespace/slashes from declared slugs")
	}
}

func TestNewIndexWiresConfiguredVerificationSlugs(t *testing.T) {
	idx, err := NewIndex(config.Config{TechnicalVerificationSlugs: []string{"abuseipdb-verification"}})
	if err != nil {
		t.Fatal(err)
	}
	if !idx.Classifier().IsTechnical(Page{Slug: "abuseipdb-verification"}) {
		t.Fatal("NewIndex must wire config.TechnicalVerificationSlugs into the classifier")
	}
}

// This is the correctness property a package-level global would have
// broken: building a second index with a *different* (empty) config must
// never retroactively change what the first, already-built index
// classifies. Each index's allowlist must stay pinned to the config it was
// actually built from.
func TestTwoIndexesWithDifferentConfigsDoNotShareVerificationAllowlist(t *testing.T) {
	withSlug, err := NewIndex(config.Config{TechnicalVerificationSlugs: []string{"abuseipdb-verification"}})
	if err != nil {
		t.Fatal(err)
	}
	without, err := NewIndex(config.Config{})
	if err != nil {
		t.Fatal(err)
	}

	if !withSlug.Classifier().IsTechnical(Page{Slug: "abuseipdb-verification"}) {
		t.Fatal("the index built with the slug configured must still classify it as technical after a second, differently-configured index was built")
	}
	if without.Classifier().IsTechnical(Page{Slug: "abuseipdb-verification"}) {
		t.Fatal("the index built without the slug configured must not classify it as technical")
	}
}
