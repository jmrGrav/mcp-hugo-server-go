package admin

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/changeset"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

// TestClassifyDirtyPorcelainResourceClasses is the #864 regression test: git
// porcelain lines must be classified into safe coarse resource classes, and
// the classifier must never surface a path fragment — only the stable class
// labels. This proves an orphaned generated asset (and other residue) is
// diagnosable by class without exposing raw paths (#775 invariant preserved).
func TestClassifyDirtyPorcelainResourceClasses(t *testing.T) {
	lines := []string{
		" M content/posts/hello/index.md",                // content_source
		"?? static/images/posts/hello" + HeroImageSuffix, // generated_asset (orphaned hero)
		" M layouts/partials/head.html",                  // external_unknown
		"R  content/a/index.md -> content/b/index.md",    // rename → content_source (dest)
		"?? somewhere/mcp-preview-abc/index.html",        // preview_residue
	}
	got := classifyDirtyPorcelain(lines)
	want := []string{
		dirtyClassContentSource,
		dirtyClassExternalUnknown,
		dirtyClassGeneratedAsset,
		dirtyClassPreviewResidue,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirty_classes = %v, want %v (sorted, de-duplicated)", got, want)
	}

	// Path-leak guard: no returned label may contain a path fragment.
	for _, c := range got {
		for _, frag := range []string{"content/", "static/", "hello", "layouts", "index.md", "mcp-preview"} {
			if strings.Contains(c, frag) {
				t.Fatalf("dirty class %q leaked path fragment %q", c, frag)
			}
		}
	}
}

func TestClassifyDirtyPathShapes(t *testing.T) {
	cases := map[string]string{
		"content/posts/x/index.md":                 dirtyClassContentSource,
		"index.fr.md":                              dirtyClassContentSource,
		"static/images/x" + HeroImageSuffix:        dirtyClassGeneratedAsset,
		"static/images/nested/y" + HeroImageSuffix: dirtyClassGeneratedAsset,
		"static/css/site.css":                      dirtyClassExternalUnknown,
		"config.toml":                              dirtyClassExternalUnknown,
		"tmp/mcp-preview-xyz/page.html":            dirtyClassPreviewResidue,
	}
	for path, want := range cases {
		if got := classifyDirtyPath(path); got != want {
			t.Errorf("classifyDirtyPath(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestComputePublicationSafetyExternalUnknownIsUnsafe is #1142's direct
// coverage for the case nothing in the server-level tests reaches: a
// pending page no change-set this process has recorded a mutation for
// (changeset.Registry.OwnerOfSourceKey returns ok=false). This is exactly
// what happens to every pending page on a fresh process after a restart —
// mutation attribution is process-lifetime-only (see Registry's own doc
// comment) — so this is the common case on a redeployed server with
// unpublished work still sitting in content_root, not an edge case.
// SafeToPublish must be false here even though guardForeignChangeSet itself
// would let this exact page through unblocked (an unowned pending page is
// never "foreign" to that guard) — this field is deliberately stricter,
// see runtime_status.go's own doc comment on publicationSafetyRuntimeStatus.
func TestComputePublicationSafetyExternalUnknownIsUnsafe(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/untracked", BuildPending: true})

	changeSets := changeset.NewRegistry(nil)
	ctx := context.Background()

	result, err := computePublicationSafety(ctx, changeSets, srcIdx, "", 0)
	if err != nil {
		t.Fatalf("computePublicationSafety() error = %v", err)
	}
	if result.ExternalUnknownChanges != 1 {
		t.Fatalf("ExternalUnknownChanges = %d, want 1", result.ExternalUnknownChanges)
	}
	if result.CurrentChangeSet.Changes != 0 {
		t.Fatalf("CurrentChangeSet.Changes = %d, want 0 (the pending page belongs to no tracked change-set)", result.CurrentChangeSet.Changes)
	}
	if result.SafeToPublish {
		t.Fatal("SafeToPublish = true with an untracked pending page present, want false — an untracked change might not be the caller's own")
	}
	if result.UnpublishedChangesCount != 1 {
		t.Fatalf("UnpublishedChangesCount = %d, want 1", result.UnpublishedChangesCount)
	}
}

// TestComputePublicationSafetyDoesNotDoubleCountOrdinaryMCPWork guards the
// caller contract of the externalOutOfBandPending parameter: the handler
// (registerRuntimeStatus) is responsible for excluding any page with
// BuildPending==true or a known change-set owner before counting it into
// externalOutOfBandPending — computePublicationSafety trusts that
// pre-filtering and adds the count directly (see its own doc comment). This
// test proves the caller-side exclusion actually holds for the common case
// this whole fix exists to not regress: ordinary in-progress MCP editing,
// with no drift at all, must not report SafeToPublish=false.
func TestComputePublicationSafetyDoesNotDoubleCountOrdinaryMCPWork(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	srcIdx.Upsert(hugosite.SourcePage{Slug: "posts/tracked", BuildPending: true})

	changeSets := changeset.NewRegistry(nil)
	ctx := context.Background()
	now := time.Now().UTC()
	changeSets.RecordMutation("default:unknown", "unknown", "create_page", "posts/tracked", "create", now)

	// externalOutOfBandPending == 0: "posts/tracked" has BuildPending==true
	// and a known owner, so the handler's own filtering (BuildPending==false
	// AND unowned) would never have included it here in the first place —
	// this call mirrors exactly what the handler would actually pass.
	result, err := computePublicationSafety(ctx, changeSets, srcIdx, "", 0)
	if err != nil {
		t.Fatalf("computePublicationSafety() error = %v", err)
	}
	if result.ExternalUnknownChanges != 0 {
		t.Fatalf("ExternalUnknownChanges = %d, want 0 (fully attributed to the current change-set, not real drift)", result.ExternalUnknownChanges)
	}
	if !result.SafeToPublish {
		t.Fatal("SafeToPublish = false with only ordinary attributed MCP work pending, want true — must not false-positive on normal editing")
	}
}

// TestComputePublicationSafetyDetectsOutOfBandDriftBeyondChangeSets is the
// direct regression test for the bug an external live audit caught: a page
// that drifted via a direct filesystem/git edit — never touched by this
// process's own write tools — never carries a BuildPending flag, so
// srcIdx.PendingPages() alone can never see it, even though
// data.source_ahead_reason on the very same get_runtime_status response
// already reports out_of_band_source_drift. Confirmed live: safe_to_publish
// stayed true while source_ahead_reason said out_of_band_source_drift with
// unpublished_changes_count:2. With no change-set activity at all
// (attributed == 0) and externalOutOfBandPending == 2 (what the resolver
// walk found), the full 2 must now surface as unattributed drift.
func TestComputePublicationSafetyDetectsOutOfBandDriftBeyondChangeSets(t *testing.T) {
	srcIdx, err := hugosite.NewSourceIndex(t.TempDir())
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	// No srcIdx.Upsert with BuildPending at all: this process tracked
	// nothing itself — the drift is entirely external, exactly the
	// production scenario (a fresh process, or an edit this process never
	// touched).
	changeSets := changeset.NewRegistry(nil)
	ctx := context.Background()

	result, err := computePublicationSafety(ctx, changeSets, srcIdx, "", 2)
	if err != nil {
		t.Fatalf("computePublicationSafety() error = %v", err)
	}
	if result.ExternalUnknownChanges != 2 {
		t.Fatalf("ExternalUnknownChanges = %d, want 2 (unattributed out-of-band drift)", result.ExternalUnknownChanges)
	}
	if result.SafeToPublish {
		t.Fatal("SafeToPublish = true with unattributed out-of-band drift present, want false")
	}
}
