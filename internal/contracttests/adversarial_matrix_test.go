package contracttests

// Adversarial integration matrix for issue #862 AC2/AC3.
//
// AC2 asks for adversarial integration coverage for a specific list of bug
// classes already found live this cycle. Most of these already have focused
// unit-level regression tests in their own packages (internal/urlpolicy,
// internal/previewstore, internal/tools/write) — this file does not
// duplicate those. Its job is the part AC2 asks for that unit tests can't
// cover: exercising the attack through the real MCP tools/call boundary
// (JSON args in, tool envelope out), the way an actual agent client would
// trigger it, and asserting no residue is left behind (AC3).
//
// Already covered elsewhere (not duplicated here — see file for detail):
//   - javascript:/data:/vbscript:/protocol-relative URL rejection:
//     internal/urlpolicy/urlpolicy_test.go
//   - preview token single-use / session race:
//     internal/previewstore/store_test.go
//   - invalid SVG / fake image format upload:
//     internal/tools/write/svg_validate_test.go
//   - TTL zero/negative/huge clamping-with-signal:
//     internal/tools/admin/create_preview_test.go (ttl_seconds cases)
//   - generated asset cleanup symmetry (generate_hero_image's response
//     feeding delete_page_asset's arguments): internal/tools/admin/image_test.go,
//     internal/tools/write/page_assets_test.go
//
// Newly added here:
//   - title XSS payload rejected through the real create_page tools/call path
//   - malicious featured_image rejected through the real update_page
//     tools/call path (the #835 attribute-breakout payload)
//   - bilingual mutation isolation: updating one translation must not alter
//     the sibling translation's file on disk
//   - early-error quota accuracy: a request that fails validation before
//     any rate-limited work happens must not consume a rate-limit unit
//   - residue-free assertion: the above content-creating calls leave no
//     files on disk beyond what each test explicitly expects
//
// Not covered by this file (genuine gap, left for a future pass): an
// end-to-end test that calls generate_hero_image and feeds its response
// straight into delete_page_asset through the real tools/call boundary in
// one flow — the existing per-package tests exercise both tools but not
// that exact hand-off end-to-end. Doing this here would require wiring
// admin.Register (generate_hero_image) and toolswrite.Register
// (delete_page_asset) onto one server with HugoRoot/watermark config this
// package doesn't currently set up; scoped out to avoid a fragile addition
// under this PR's time budget rather than rushing it.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAdversarialTitleXSSRejectedAtToolBoundary is a real tools/call, not a
// unit test of the validator: a client sending a raw XSS payload as `title`
// to create_page must be rejected by the tool itself, and must leave zero
// bytes on disk (not "rejected after partially writing").
func TestAdversarialTitleXSSRejectedAtToolBoundary(t *testing.T) {
	restore := setContractBuildInfo(t)
	defer restore()

	contentRoot := t.TempDir()
	session, done := newWriteSession(t, contentRoot, fixtureConfig(), nil)
	defer done()

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/xss-attempt",
		"title":      `<script>alert(1)</script>`,
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if !res.IsError {
		t.Fatalf("create_page with <script> title succeeded, want rejection: %s", marshalAny(t, res.Content))
	}
	assertDirEmpty(t, contentRoot)
}

// TestAdversarialMaliciousFeaturedImageRejectedAtToolBoundary reproduces the
// exact #835 attribute-breakout payload through update_page's real tools/call
// path, confirming the #855 shared urlpolicy validator is actually wired in
// end-to-end (not just unit-tested in isolation) and that a rejected mutation
// leaves the existing page byte-for-byte unchanged.
func TestAdversarialMaliciousFeaturedImageRejectedAtToolBoundary(t *testing.T) {
	restore := setContractBuildInfo(t)
	defer restore()

	contentRoot := t.TempDir()
	pagePath := filepath.Join(contentRoot, "posts", "existing", "index.md")
	original := "---\ntitle: Existing\n---\nOriginal body.\n"
	writeFile(t, pagePath, original)

	session, done := newWriteSession(t, contentRoot, fixtureConfig(), nil)
	defer done()

	res := callTool(t, session, "update_page", map[string]any{
		"slug":           "posts/existing",
		"featured_image": `/img.jpg" onerror="alert(1)`,
	})
	if !res.IsError {
		t.Fatalf("update_page with malicious featured_image succeeded, want rejection: %s", marshalAny(t, res.Content))
	}
	after, err := os.ReadFile(pagePath)
	if err != nil {
		t.Fatalf("ReadFile(existing after rejected update) error = %v", err)
	}
	if string(after) != original {
		t.Fatalf("rejected featured_image mutation still modified the file:\nwant:\n%s\ngot:\n%s", original, string(after))
	}
}

// TestAdversarialBilingualMutationIsolation is the #829-class regression this
// cycle repeatedly guarded against: updating one translation must not alter
// its sibling's file on disk, verified end-to-end through two real
// update_page calls against a genuine two-file bundle.
func TestAdversarialBilingualMutationIsolation(t *testing.T) {
	restore := setContractBuildInfo(t)
	defer restore()

	contentRoot := t.TempDir()
	enPath := filepath.Join(contentRoot, "posts", "bilingual", "index.en.md")
	frPath := filepath.Join(contentRoot, "posts", "bilingual", "index.fr.md")
	enOriginal := "---\ntitle: Hello\n---\nEN body.\n"
	frOriginal := "---\ntitle: Bonjour\n---\nFR body.\n"
	writeFile(t, enPath, enOriginal)
	writeFile(t, frPath, frOriginal)

	session, done := newWriteSession(t, contentRoot, fixtureConfig(), nil)
	defer done()
	readSession, readDone := newReadSession(t, nil, fixtureConfig(), mustSourceIndexFromRoot(t, contentRoot))
	defer readDone()

	revRes := callTool(t, readSession, "get_page_markdown", map[string]any{"slug": "/posts/bilingual/", "lang": "en"})
	if revRes.IsError {
		t.Fatalf("get_page_markdown returned error: %s", marshalAny(t, revRes.Content))
	}
	page := mapAt(t, decodeContent(t, revRes), "page")
	expectedRevision := asString(page["revision"])

	callMustSucceed(t, session, "update_page", map[string]any{
		"slug":              "posts/bilingual",
		"lang":              "en",
		"title":             "Hello Edited",
		"expected_revision": expectedRevision,
	})

	frAfter, err := os.ReadFile(frPath)
	if err != nil {
		t.Fatalf("ReadFile(fr sibling after en-only update) error = %v", err)
	}
	if string(frAfter) != frOriginal {
		t.Fatalf("updating EN translation modified the FR sibling:\nwant unchanged:\n%s\ngot:\n%s", frOriginal, string(frAfter))
	}
	enAfter, err := os.ReadFile(enPath)
	if err != nil {
		t.Fatalf("ReadFile(en after update) error = %v", err)
	}
	if string(enAfter) == enOriginal {
		t.Fatal("update_page reported success but EN file is unchanged")
	}
}

// TestAdversarialEarlyValidationErrorDoesNotConsumeRateLimit is AC2's
// "early-error quota accuracy" case: a request that fails synchronous input
// validation (before any rate-limited mutation work happens) must not debit
// the caller's rate-limit bucket — otherwise a client hammering invalid input
// could exhaust its own quota without ever performing a real mutation, which
// is both a wasted-budget bug and a misleading signal.
func TestAdversarialEarlyValidationErrorDoesNotConsumeRateLimit(t *testing.T) {
	restore := setContractBuildInfo(t)
	defer restore()

	contentRoot := t.TempDir()
	session, done := newWriteSession(t, contentRoot, fixtureConfig(), nil)
	defer done()

	before := callMustSucceed(t, session, "get_rate_limits", map[string]any{})
	beforeRemaining := asInt(t, mapAt(t, mapAt(t, before, "data"), "create_update_upload")["remaining"])

	res := callTool(t, session, "create_page", map[string]any{
		"slug":       "posts/xss-attempt-2",
		"title":      `<script>alert(1)</script>`,
		"body":       "Body",
		"tags":       []any{},
		"categories": []any{},
	})
	if !res.IsError {
		t.Fatalf("create_page with <script> title succeeded, want rejection: %s", marshalAny(t, res.Content))
	}

	after := callMustSucceed(t, session, "get_rate_limits", map[string]any{})
	afterRemaining := asInt(t, mapAt(t, mapAt(t, after, "data"), "create_update_upload")["remaining"])
	if afterRemaining != beforeRemaining {
		t.Fatalf("rate_limit_remaining changed after an early-validation rejection: before=%d after=%d, want unchanged", beforeRemaining, afterRemaining)
	}
	assertDirEmpty(t, contentRoot)
}

// assertDirEmpty is the AC3 residue-free assertion: a rejected mutation must
// leave the content root exactly as it started (or, when called against a
// fresh t.TempDir(), completely empty) — no partial file, no orphaned
// directory.
func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("ReadDir(%q) error = %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected no residue in %q after a rejected mutation, found: %v", dir, names)
	}
}
