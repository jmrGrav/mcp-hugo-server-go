package admin

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/changeset"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

// guardForeignChangeSet is #1140's pre-flight refusal: build_site has no
// mechanism to build one change-set's pages and not another's (a single
// Hugo pass renders the whole content tree), so the only way to keep one
// principal's in-flight edit from publishing another's concurrently-made
// edit under the same shared credentials is to refuse the build outright
// when a pending page is known to belong to a change-set the caller hasn't
// explicitly acknowledged. This is the direct regression guard for the
// 2026-08-14 incident (two agents sharing one OAuth principal, one bumping
// Hugo while the other edited posts/csp-nonce, corrupting it).
//
// changeSetID is resolved the same way every mutation tool resolves it
// (blank -> implicit per-principal default). changeSetIDs is the escape
// hatch the issue itself specifies for the case that actually matters most:
// two (or more) change-sets both have real pending work at once — the
// normal state of affairs the instant two agents are genuinely editing
// concurrently, which is this feature's entire target scenario. Without a
// way to acknowledge more than one change-set at once, that scenario would
// deadlock: neither agent's own single-change-set build could ever
// succeed, since the other's pending page is always foreign to it, and
// nothing but a full server restart clears BuildPending. Passing both ids
// in changeSetIDs is the caller affirmatively saying "yes, I know about
// both of these, publish them together" — every id in the set is
// separately resolved (and so separately ownership-checked) before any
// page is considered acknowledged.
//
// If both changeSetID and changeSetIDs are given, changeSetIDs wins and
// changeSetID is ignored (rather than silently unioned, which would make
// the two fields do the same thing two different ways).
//
// changeSets == nil or srcIdx == nil (a caller that never wired the
// change-set registry, or a build with no source index at all) skips the
// check entirely — this guard is purely additive on top of #1135's
// opt-in change_set_id parameter, never a new hard requirement for every
// deployment shape.
//
// What this guard does NOT do, deliberately (see changeset.Registry's own
// OwnerOfSourceKey doc comment for why): it cannot tell "this pending page
// was never touched by any change-set this process tracked" apart from
// "this pending page was edited outside the MCP server entirely (direct
// filesystem/SSH write)" or "was edited by a change-set from before this
// process restarted." An unowned pending page is therefore always allowed
// through — full external-source-drift detection (the issue's
// EXTERNAL_SOURCE_DRIFT scenario) needs the per-file fingerprinting #1141
// adds, which this issue does not implement. It also inspects the same
// volatile BuildPending bookkeeping build_site itself falls back to
// without db_path — on a db_path-configured deployment, the build's
// actual page set is instead derived from on-disk source fingerprints
// (see build_site's own doc comment), which can differ from what this
// guard inspected; #1141's revision guards are the intended place to
// close that gap.
func guardForeignChangeSet(ctx context.Context, changeSets *changeset.Registry, srcIdx *hugosite.SourceIndex, changeSetID string, changeSetIDs []string) error {
	if changeSets == nil || srcIdx == nil {
		return nil
	}
	requested := changeSetIDs
	if len(requested) == 0 {
		requested = []string{changeSetID}
	}
	now := time.Now().UTC()
	acknowledged := make(map[string]bool, len(requested))
	for _, id := range requested {
		resolved, err := changeSets.Resolve(ctx, id, now)
		if err != nil {
			return err
		}
		acknowledged[resolved] = true
	}

	// PendingPages reads SourceIndex's unsynchronized internal slice/maps;
	// hugosite.ContentMu is what serializes that against concurrent
	// create_page/update_page/delete_page writers (see ContentMu's own doc
	// comment). runBuild itself takes the same lock immediately after this
	// guard returns — held only long enough to snapshot the pending set,
	// not across the whole guard, so this doesn't widen the lock's scope
	// beyond a single read.
	hugosite.ContentMu.RLock()
	pending := srcIdx.PendingPages()
	hugosite.ContentMu.RUnlock()

	seen := make(map[string]bool)
	var foreign []string
	for _, p := range pending {
		if seen[p.Slug] {
			continue
		}
		owner, ok := changeSets.OwnerOfSourceKey(p.Slug)
		if ok && !acknowledged[owner] {
			foreign = append(foreign, p.Slug)
			seen[p.Slug] = true
		}
	}
	if len(foreign) == 0 {
		return nil
	}
	sort.Strings(foreign)
	// Machine code is lowercase_snake_case to match this codebase's actual
	// error-code convention (build_in_progress, invalid_params, ...) — see
	// toolcontract.isMachineCode, which only recognizes [a-z0-9_]. The
	// issue's own example used FOREIGN_CHANGE_SET_PRESENT; the semantics
	// are identical, only the casing is normalized to what the response
	// envelope actually parses as a machine code.
	return fmt.Errorf(
		"foreign_change_set_present: %d pending page(s) belong to a change-set not among the ones this call acknowledged: %s — publish/build only what your own change_set_id(s) touched, or pass every change-set with pending work in change_set_ids to publish them together. This check only catches pending pages this same running process has tracked mutations for; it does not detect edits made outside this server (direct filesystem writes) or before this process last restarted — see #1141 for stronger revision guards",
		len(foreign), strings.Join(foreign, ", "),
	)
}
