// Package changeset implements the change_set_id ownership and mutation
// attribution primitive introduced by #1135, and the foreign-change-set
// pending-page lookup #1140 builds on top of it. It is a standalone package
// (rather than living inside internal/tools/write, where #1135 first
// shipped it) because #1140 needs the exact same in-memory mutation record
// read from internal/tools/admin's build_site/publish_changes handlers —
// two packages both need to see one shared registry instance, not two
// independently-tracking copies.
package changeset

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/caller"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
)

// idByteLen matches previewstore.NewID's construction: 16 raw bytes -> 32
// hex chars, crypto/rand because a caller-presented change_set_id doubles
// as the ownership check itself (#1135) — an attacker who could guess or
// brute-force one could attribute mutations to, or (#1140) publish,
// someone else's change-set.
const idByteLen = 16

// NewID returns a random, opaque, URL-safe identifier, prefixed so it's
// visually distinguishable from other opaque IDs in logs/errors (preview
// IDs, idempotency keys) without implying any structure a caller could rely
// on beyond "starts with cs_".
func NewID() (string, error) {
	b := make([]byte, idByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "cs_" + hex.EncodeToString(b), nil
}

// DefaultID is the stable implicit change-set used whenever a caller never
// supplies change_set_id — every mutation tool's `data` still reports it so
// a client that later starts scoping publication by change-set can see
// what it was already implicitly using. Deliberately not random or SQLite
// registered: it is not a security boundary by itself (a caller can't
// "steal" another principal's default bucket merely by knowing this naming
// scheme, since #1140's foreign-change-set check is always by principal
// match, never by an id's unguessability alone), it exists purely so
// pre-#1135 clients that never adopt the new parameter keep behaving
// exactly as before — one implicit change-set per principal, matching what
// mutationCallerKey already scoped everything else (rate limits,
// idempotency) to.
func DefaultID(principalID string) string {
	return "default:" + principalID
}

type owner struct {
	PrincipalID string
	CreatedAt   time.Time
	LastUsedAt  time.Time
	// DeclaredUntrustedDerivation and DeclaredUntrustedNote are a caller
	// self-report (see Registry.SetDeclaredUntrustedDerivation) — never
	// verified by this registry, never a basis for gating any tool's
	// behavior.
	DeclaredUntrustedDerivation bool
	DeclaredUntrustedNote       string
}

// Registry tracks which principal owns each explicit change_set_id and
// records per-mutation attribution. Mirrors idempotencyStore's
// persistent-optional shape (#1086) deliberately: the in-memory maps are
// always authoritative for the running process — both ownership AND the
// per-mutation attribution list are recorded in memory unconditionally, so
// neither the ownership-check nor the attribution-recording property this
// backs (and that #1140/#1142 build on) ever depends on db_path being set.
//
// SQLite persistence (only when db_path is configured) is best-effort and
// asymmetric across the two maps: owners are lazily rehydrated from SQLite
// in Resolve() on a cache miss, so ownership survives a restart. Mutations
// are NOT rehydrated — mutations and MutationsFor()/OwnerOfSourceKey() are
// process-lifetime only. After a restart, SQLite's change_set_mutations
// table (queried via db.ListChangeSetMutations) is the durable record; a
// future feature that needs mutation history to survive a restart must
// read that table directly. This also means #1140's foreign-change-set
// guard only protects concurrent editors within one running process's
// uptime — the exact shape of the incident it targets (two live agents
// editing during the same session) — not drift introduced before this
// process started or via a channel this registry never observes (direct
// filesystem/SSH edits): see OwnerOfSourceKey's own doc comment.
//
// mutations is also unbounded today — acceptable at current volume, but
// the first consumer that makes it load-bearing at scale should add
// pruning, matching idempotencyStore's maxEntries/pruneLocked pattern.
type Registry struct {
	mu         sync.Mutex
	owners     map[string]owner
	mutations  map[string][]db.ChangeSetMutation
	persistent *db.DB
}

func NewRegistry(persistent *db.DB) *Registry {
	return &Registry{
		owners:     make(map[string]owner),
		mutations:  make(map[string][]db.ChangeSetMutation),
		persistent: persistent,
	}
}

// Create mints a fresh change-set owned by principalID. SQLite persistence
// (when configured) is best-effort, matching Resolve()'s TouchChangeSet
// treatment: a durability failure here must never prevent the caller from
// learning its own newly minted id, since the in-memory registry is
// already authoritative for the running process regardless.
func (r *Registry) Create(principalID string, now time.Time) (string, error) {
	if principalID == "" {
		return "", fmt.Errorf("internal_error: cannot create a change-set without a caller identity")
	}
	id, err := NewID()
	if err != nil {
		return "", fmt.Errorf("internal_error: failed to generate change_set_id")
	}
	r.mu.Lock()
	r.owners[id] = owner{PrincipalID: principalID, CreatedAt: now, LastUsedAt: now}
	r.mu.Unlock()
	if r.persistent != nil {
		if err := r.persistent.CreateChangeSet(id, principalID, now); err != nil {
			slog.Warn("create_change_set: could not persist change-set ownership", "change_set_id", id, "error", err)
		}
	}
	return id, nil
}

// SetDeclaredUntrustedDerivation records changeSetID's self-reported
// untrusted-derivation state, set at most once at creation time by
// registerCreateChangeSet's handler. This is a caller self-report, not
// something this registry (or anything else server-side) verifies: the
// server never sees what informed the arguments of a later
// create_page/update_page call, so an agent that already decided to act on
// injected content can simply omit this declaration. Its sole purpose is
// audit visibility — surfaced read-only via
// get_runtime_status.data.publication_safety.current_change_set (§6.18,
// §6.27) for a human or downstream tooling to see before publishing. MUST
// NOT be used to gate any tool's behavior (see docs/mcp-contract.md §6.27
// and SECURITY.md for why: gating on a self-report only punishes honest
// declarations while doing nothing against a dishonest omission).
func (r *Registry) SetDeclaredUntrustedDerivation(changeSetID string, declared bool, note string) {
	r.mu.Lock()
	o := r.owners[changeSetID]
	o.DeclaredUntrustedDerivation = declared
	o.DeclaredUntrustedNote = note
	r.owners[changeSetID] = o
	r.mu.Unlock()
	if r.persistent != nil {
		if err := r.persistent.SetChangeSetDeclaredUntrustedDerivation(changeSetID, declared, note); err != nil {
			slog.Warn("create_change_set: could not persist declared-untrusted-derivation", "change_set_id", changeSetID, "error", err)
		}
	}
}

// DeclaredUntrustedDerivation returns changeSetID's self-reported
// untrusted-derivation state (false, "" if never declared, or if
// changeSetID is unknown to this process — e.g. an implicit per-principal
// default bucket, which was never created via create_change_set and so
// can never carry a declaration).
func (r *Registry) DeclaredUntrustedDerivation(changeSetID string) (declared bool, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.owners[changeSetID]
	if !ok {
		return false, ""
	}
	return o.DeclaredUntrustedDerivation, o.DeclaredUntrustedNote
}

// Resolve validates a caller-supplied change_set_id, or — if requested is
// blank — returns the implicit stable per-principal default (see
// DefaultID) so callers that never adopt this parameter are unaffected. A
// non-blank id that doesn't exist, or exists but belongs to a different
// principal, is rejected with the same error either way: this never
// confirms or denies that a given id belongs to *someone else*, only that
// it isn't usable by this caller.
func (r *Registry) Resolve(ctx context.Context, requested string, now time.Time) (string, error) {
	resolved, err := r.checkOwnership(ctx, requested)
	if err != nil {
		return "", err
	}
	if requested == "" {
		return resolved, nil
	}
	r.mu.Lock()
	updated := r.owners[requested]
	updated.LastUsedAt = now
	r.owners[requested] = updated
	r.mu.Unlock()
	if r.persistent != nil {
		_ = r.persistent.TouchChangeSet(requested, now)
	}
	return resolved, nil
}

// Peek is Resolve without the LastUsedAt bookkeeping side effect (no
// in-memory update, no best-effort SQLite touch) — for read-only callers
// like #1142's publication-safety reporting on get_runtime_status, which
// carries ReadOnlyHint:true and so must not mutate change-set state merely
// by being asked about it. Ownership rules are otherwise identical to
// Resolve.
func (r *Registry) Peek(ctx context.Context, requested string) (string, error) {
	return r.checkOwnership(ctx, requested)
}

// checkOwnership is Resolve/Peek's shared validation: blank resolves to the
// caller's implicit default bucket; anything else must be owned by the
// caller (rehydrating from SQLite on a cache miss, same as Resolve always
// has), or it's rejected without confirming/denying that a given id belongs
// to *someone else*.
func (r *Registry) checkOwnership(ctx context.Context, requested string) (string, error) {
	principalID := caller.MutationKey(ctx)
	if requested == "" {
		return DefaultID(principalID), nil
	}
	r.mu.Lock()
	o, ok := r.owners[requested]
	r.mu.Unlock()
	if !ok && r.persistent != nil {
		persistedPrincipal, declared, note, found, err := r.persistent.GetChangeSetOwner(requested)
		if err != nil {
			return "", fmt.Errorf("internal_error: failed to look up change_set_id")
		}
		if found {
			o = owner{PrincipalID: persistedPrincipal, DeclaredUntrustedDerivation: declared, DeclaredUntrustedNote: note}
			ok = true
			r.mu.Lock()
			r.owners[requested] = o
			r.mu.Unlock()
		}
	}
	if !ok || o.PrincipalID != principalID {
		return "", fmt.Errorf("invalid_params: change_set_id is unknown or does not belong to this caller — call create_change_set to obtain one")
	}
	return requested, nil
}

// RecordMutation attributes one already-successful mutation to its resolved
// change-set. Always recorded in-memory, regardless of db_path — #1140's
// foreign-change-set computation must work on every deployment, not just
// db_path-configured ones. SQLite persistence (when configured) is
// additionally attempted, best-effort: a durability failure here must never
// fail the mutation itself, which has already landed on disk by the time
// this is called. This method itself doesn't return an error at all,
// deliberately, so every mutation tool call-site can invoke it
// unconditionally without another branch to handle.
func (r *Registry) RecordMutation(changeSetID, principalID, tool, sourceKey, mutationType string, now time.Time) {
	entry := db.ChangeSetMutation{
		ChangeSetID:  changeSetID,
		PrincipalID:  principalID,
		Tool:         tool,
		SourceKey:    sourceKey,
		MutationType: mutationType,
		CreatedAt:    now,
	}
	r.mu.Lock()
	r.mutations[changeSetID] = append(r.mutations[changeSetID], entry)
	r.mu.Unlock()
	if r.persistent != nil {
		if err := r.persistent.RecordChangeSetMutation(entry); err != nil {
			slog.Warn("change-set mutation attribution: could not persist", "change_set_id", changeSetID, "tool", tool, "error", err)
		}
	}
}

// MutationsFor returns every mutation recorded against changeSetID in this
// process, oldest first — the in-memory record, which is always populated
// regardless of db_path (see Registry's own doc comment).
func (r *Registry) MutationsFor(changeSetID string) []db.ChangeSetMutation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]db.ChangeSetMutation, len(r.mutations[changeSetID]))
	copy(out, r.mutations[changeSetID])
	return out
}

// OwnerOfSourceKey is #1140's foreign-change-set lookup: across every
// change-set this process has tracked mutations for, which one most
// recently mutated sourceKey, and under which change_set_id. Returns
// ok=false if no tracked mutation touched sourceKey at all — which the
// caller must treat as "no known foreign owner, allow", NOT as proof the
// page was never changed by anyone else. This registry has no channel for
// observing edits it didn't originate (direct filesystem/SSH writes) and
// its mutation record doesn't survive a restart (see Registry's own doc
// comment), so an unowned pending page is genuinely ambiguous between
// "nobody else touched it" and "something touched it this registry never
// saw." #1140 only promises to catch the former case turning into the
// latter — two change-sets both tracked by this same live process — not
// full external-source-drift detection, which needs the per-file
// fingerprinting #1141 adds.
func (r *Registry) OwnerOfSourceKey(sourceKey string) (changeSetID string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var latest time.Time
	for csID, entries := range r.mutations {
		for _, e := range entries {
			if e.SourceKey != sourceKey {
				continue
			}
			if !ok || e.CreatedAt.After(latest) {
				changeSetID = csID
				latest = e.CreatedAt
				ok = true
			}
		}
	}
	return changeSetID, ok
}
