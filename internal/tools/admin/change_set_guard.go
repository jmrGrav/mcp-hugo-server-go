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
// when a pending page is known to belong to a change-set other than the
// caller's resolved one. This is the direct regression guard for the
// 2026-08-14 incident (two agents sharing one OAuth principal, one bumping
// Hugo while the other edited posts/csp-nonce, corrupting it).
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
// adds, which this issue does not implement.
func guardForeignChangeSet(ctx context.Context, changeSets *changeset.Registry, srcIdx *hugosite.SourceIndex, requestedChangeSetID string) error {
	if changeSets == nil || srcIdx == nil {
		return nil
	}
	resolved, err := changeSets.Resolve(ctx, requestedChangeSetID, time.Now().UTC())
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	var foreign []string
	for _, p := range srcIdx.PendingPages() {
		if seen[p.Slug] {
			continue
		}
		owner, ok := changeSets.OwnerOfSourceKey(p.Slug)
		if ok && owner != resolved {
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
		"foreign_change_set_present: %d pending page(s) belong to a change-set other than the one this call resolved to (%s): %s — publish/build only what your own change_set_id touched; another change-set's owner must publish those separately. This check only catches pending pages this same running process has tracked mutations for; it does not detect edits made outside this server (direct filesystem writes) or before this process last restarted — see #1141 for stronger revision guards",
		len(foreign), resolved, strings.Join(foreign, ", "),
	)
}
