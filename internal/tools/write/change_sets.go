package write

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/db"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// changeSetIDByteLen matches previewstore.NewID's construction: 16 raw
// bytes -> 32 hex chars, crypto/rand because a caller-presented
// change_set_id doubles as the ownership check itself (#1135) — an
// attacker who could guess or brute-force one could attribute mutations
// to, or later (per #1140) publish, someone else's change-set.
const changeSetIDByteLen = 16

// newChangeSetID returns a random, opaque, URL-safe identifier, prefixed so
// it's visually distinguishable from other opaque IDs in logs/errors (preview
// IDs, idempotency keys) without implying any structure a caller could rely
// on beyond "starts with cs_".
func newChangeSetID() (string, error) {
	b := make([]byte, changeSetIDByteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "cs_" + hex.EncodeToString(b), nil
}

// defaultChangeSetID is the stable implicit change-set used whenever a
// caller never supplies change_set_id — every mutation tool's `data`
// still reports it so a client that later starts scoping publication by
// change-set can see what it was already implicitly using. Deliberately
// not random or SQLite-registered: it is not a security boundary by
// itself (a caller can't "steal" another principal's default bucket
// merely by knowing this naming scheme, since #1140's ownership check is
// always by principal match, never by an id's unguessability alone), it
// exists purely so pre-#1135 clients that never adopt the new parameter
// keep behaving exactly as before — one implicit change-set per
// principal, matching what mutationCallerKey already scoped everything
// else (rate limits, idempotency) to.
func defaultChangeSetID(principalID string) string {
	return "default:" + principalID
}

type changeSetOwner struct {
	PrincipalID string
	CreatedAt   time.Time
	LastUsedAt  time.Time
}

// changeSetRegistry tracks which principal owns each explicit change_set_id
// and records per-mutation attribution. Mirrors idempotencyStore's
// persistent-optional shape (#1086) deliberately: the in-memory maps are
// always authoritative for the running process — both ownership AND the
// per-mutation attribution list are recorded in memory unconditionally, so
// neither the ownership-check nor the attribution-recording property this
// backs (and that #1140/#1142 build on) ever depends on db_path being set.
//
// SQLite persistence (only when db_path is configured) is best-effort and
// asymmetric across the two maps: owners are lazily rehydrated from SQLite
// in resolve() on a cache miss, so ownership survives a restart. Mutations
// are NOT rehydrated — r.mutations and mutationsFor() are process-lifetime
// only. After a restart, SQLite's change_set_mutations table (queried via
// db.ListChangeSetMutations) is the durable record; any future feature
// (#1140/#1142) that needs mutation history to survive a restart must read
// that table directly rather than mutationsFor(). r.mutations is also
// unbounded today — acceptable at current volume, but the first consumer
// that makes it load-bearing should add pruning, matching idempotencyStore's
// maxEntries/pruneLocked pattern.
type changeSetRegistry struct {
	mu         sync.Mutex
	owners     map[string]changeSetOwner
	mutations  map[string][]db.ChangeSetMutation
	persistent *db.DB
}

func newChangeSetRegistry(persistent *db.DB) *changeSetRegistry {
	return &changeSetRegistry{
		owners:     make(map[string]changeSetOwner),
		mutations:  make(map[string][]db.ChangeSetMutation),
		persistent: persistent,
	}
}

// create mints a fresh change-set owned by principalID. SQLite persistence
// (when configured) is best-effort, matching resolve()'s TouchChangeSet
// treatment: a durability failure here must never prevent the caller from
// learning its own newly minted id, since the in-memory registry is
// already authoritative for the running process regardless.
func (r *changeSetRegistry) create(principalID string, now time.Time) (string, error) {
	if principalID == "" {
		return "", fmt.Errorf("internal_error: cannot create a change-set without a caller identity")
	}
	id, err := newChangeSetID()
	if err != nil {
		return "", fmt.Errorf("internal_error: failed to generate change_set_id")
	}
	r.mu.Lock()
	r.owners[id] = changeSetOwner{PrincipalID: principalID, CreatedAt: now, LastUsedAt: now}
	r.mu.Unlock()
	if r.persistent != nil {
		if err := r.persistent.CreateChangeSet(id, principalID, now); err != nil {
			slog.Warn("create_change_set: could not persist change-set ownership", "change_set_id", id, "error", err)
		}
	}
	return id, nil
}

// resolve validates a caller-supplied change_set_id, or — if requested is
// blank — returns the implicit stable per-principal default (see
// defaultChangeSetID) so callers that never adopt this parameter are
// unaffected. A non-blank id that doesn't exist, or exists but belongs to
// a different principal, is rejected with the same error either way: this
// never confirms or denies that a given id belongs to *someone else*,
// only that it isn't usable by this caller.
func (r *changeSetRegistry) resolve(ctx context.Context, requested string, now time.Time) (string, error) {
	principalID := mutationCallerKey(ctx)
	if requested == "" {
		return defaultChangeSetID(principalID), nil
	}
	r.mu.Lock()
	owner, ok := r.owners[requested]
	r.mu.Unlock()
	if !ok && r.persistent != nil {
		persistedPrincipal, found, err := r.persistent.GetChangeSetOwner(requested)
		if err != nil {
			return "", fmt.Errorf("internal_error: failed to look up change_set_id")
		}
		if found {
			owner = changeSetOwner{PrincipalID: persistedPrincipal}
			ok = true
			r.mu.Lock()
			r.owners[requested] = owner
			r.mu.Unlock()
		}
	}
	if !ok || owner.PrincipalID != principalID {
		return "", fmt.Errorf("invalid_params: change_set_id is unknown or does not belong to this caller — call create_change_set to obtain one")
	}
	r.mu.Lock()
	o := r.owners[requested]
	o.LastUsedAt = now
	r.owners[requested] = o
	r.mu.Unlock()
	if r.persistent != nil {
		_ = r.persistent.TouchChangeSet(requested, now)
	}
	return requested, nil
}

// recordMutation attributes one already-successful mutation to its
// resolved change-set. Always recorded in-memory, regardless of db_path —
// #1140's foreign-change-set computation must work on every deployment,
// not just db_path-configured ones. SQLite persistence (when configured)
// is additionally attempted, best-effort: a durability failure here must
// never fail the mutation itself, which has already landed on disk by the
// time this is called. This method itself doesn't return an error at all,
// deliberately, so every mutation tool call-site can invoke it
// unconditionally without another branch to handle.
func (r *changeSetRegistry) recordMutation(changeSetID, principalID, tool, sourceKey, mutationType string, now time.Time) {
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

// mutationsFor returns every mutation recorded against changeSetID in this
// process, oldest first — the in-memory record, which is always populated
// regardless of db_path (see changeSetRegistry's own doc comment). Feeds
// #1140/#1142; not consumed within #1135 itself beyond its own tests.
func (r *changeSetRegistry) mutationsFor(changeSetID string) []db.ChangeSetMutation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]db.ChangeSetMutation, len(r.mutations[changeSetID]))
	copy(out, r.mutations[changeSetID])
	return out
}

type createChangeSetInput struct{}

type createChangeSetOutput struct {
	toolcontract.ToolResponse[createChangeSetData]
}

type createChangeSetData struct {
	ChangeSetID string `json:"change_set_id"`
}

func newCreateChangeSetOutput(data createChangeSetData) createChangeSetOutput {
	return createChangeSetOutput{ToolResponse: writeSuccessEnvelope(data)}
}

// registerCreateChangeSet wires create_change_set (#1135): mints an opaque,
// caller-owned change_set_id that every mutation tool's new optional
// change_set_id parameter accepts. Exists so a client obtains an
// unguessable ID from the server rather than inventing its own — a
// self-chosen id would either have to be validated against every other
// caller's ids (a global namespace collision risk) or trusted blindly
// (defeating the ownership check entirely).
func registerCreateChangeSet(s *mcp.Server, changeSets *changeSetRegistry) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "create_change_set",
		Title: "Create change-set",
		Description: "Mint a new opaque change_set_id, owned by the calling principal, to pass as the optional " +
			"change_set_id parameter on create_page/update_page/delete_page/upload_page_asset/delete_page_asset/" +
			"apply_content_plan/rollback_change/apply_bundle_plan/rollback_bundle/create_bundle/delete_bundle. " +
			"Use this when two or more clients might share the same OAuth token/principal (a realistic " +
			"single-operator deployment shape) and each needs its own mutations tracked and, once #1140 lands, " +
			"published separately — call this once per logical unit of work and reuse the returned id across " +
			"every mutation in that unit; omitting change_set_id on a mutation call falls back to a stable " +
			"implicit per-principal default, matching this server's behavior before this tool existed. " +
			"The id is opaque and durable (usable again after a reconnect) but is not itself a secret; it is " +
			"validated to belong to the calling principal on every use, never accepted from another principal.",
		InputSchema:  tools.MustSchema[createChangeSetInput](),
		OutputSchema: tools.MustSchema[createChangeSetOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, _ createChangeSetInput) (*mcp.CallToolResult, createChangeSetOutput, error) {
		principalID := mutationCallerKey(ctx)
		id, err := changeSets.create(principalID, time.Now().UTC())
		if err != nil {
			return nil, createChangeSetOutput{}, err
		}
		return nil, newCreateChangeSetOutput(createChangeSetData{ChangeSetID: id}), nil
	}))
}
