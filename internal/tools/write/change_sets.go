package write

import (
	"context"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/changeset"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type createChangeSetInput struct {
	// DeclaredUntrustedDerivation and DeclaredUntrustedNote (#1226) are an
	// optional, entirely self-reported flag: set this when the editing
	// session about to start under this change-set derives from content
	// you read that was tagged content_provenance="site_source_untrusted"
	// or "site_rendered_public_untrusted" (docs/mcp-contract.md §6.27).
	// This server cannot verify the declaration and does not gate any
	// tool's behavior on it — it exists purely so a human reviewer or
	// get_runtime_status's publication_safety can see, before publishing,
	// that a pending change-set claims untrusted derivation. Omit both
	// fields if not applicable; this has no effect on ordinary use.
	DeclaredUntrustedDerivation bool   `json:"declared_untrusted_derivation,omitempty"`
	DeclaredUntrustedNote       string `json:"declared_untrusted_note,omitempty"`
}

type createChangeSetOutput struct {
	toolcontract.ToolResponse[createChangeSetData]
}

type createChangeSetData struct {
	ChangeSetID string `json:"change_set_id"`
	// DeclaredUntrustedDerivation/DeclaredUntrustedNote echo back what was
	// recorded (see createChangeSetInput's own doc comment) so the caller
	// can confirm the self-report landed; omitted when not declared.
	DeclaredUntrustedDerivation bool   `json:"declared_untrusted_derivation,omitempty"`
	DeclaredUntrustedNote       string `json:"declared_untrusted_note,omitempty"`
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
func registerCreateChangeSet(s *mcp.Server, changeSets *changeset.Registry) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "create_change_set",
		Title: "Create change-set",
		Description: "Mint a new opaque change_set_id, owned by the calling principal, to pass as the optional " +
			"change_set_id parameter on create_page/update_page/delete_page/upload_page_asset/delete_page_asset/" +
			"apply_content_plan/rollback_change/apply_bundle_plan/rollback_bundle/create_bundle/delete_bundle, " +
			"and (#1140) on build_site/publish_changes to scope which change-set's pending pages you intend to " +
			"publish. Use this when two or more clients might share the same OAuth token/principal (a realistic " +
			"single-operator deployment shape) and each needs its own mutations tracked and published " +
			"separately — call this once per logical unit of work and reuse the returned id across every " +
			"mutation in that unit; omitting change_set_id on a mutation call falls back to a stable implicit " +
			"per-principal default, matching this server's behavior before this tool existed. " +
			"The id is opaque and durable (usable again after a reconnect) but is not itself a secret; it is " +
			"validated to belong to the calling principal on every use, never accepted from another principal. " +
			"Optional declared_untrusted_derivation/declared_untrusted_note (#1226): set declared_untrusted_derivation=true " +
			"when the editing session you're about to start under this change-set derives from content you read that " +
			"was tagged content_provenance=\"site_source_untrusted\" or \"site_rendered_public_untrusted\" " +
			"(docs/mcp-contract.md §6.27) — e.g. you're drafting a page based on something you found via search_content " +
			"or get_page_markdown. This is a self-report only: this server cannot verify it (it never sees what informed " +
			"the arguments of your later create_page/update_page calls), and no tool's behavior is gated on it. Its sole " +
			"purpose is audit visibility, surfaced read-only via get_runtime_status.data.publication_safety.current_change_set " +
			"for a human reviewer or downstream tooling to see before publishing. Omit both fields for ordinary use.",
		InputSchema:  tools.MustSchema[createChangeSetInput](),
		OutputSchema: tools.MustSchema[createChangeSetOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  false,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in createChangeSetInput) (*mcp.CallToolResult, createChangeSetOutput, error) {
		principalID := mutationCallerKey(ctx)
		id, err := changeSets.Create(principalID, time.Now().UTC())
		if err != nil {
			return nil, createChangeSetOutput{}, err
		}
		data := createChangeSetData{ChangeSetID: id}
		if in.DeclaredUntrustedDerivation || in.DeclaredUntrustedNote != "" {
			changeSets.SetDeclaredUntrustedDerivation(id, in.DeclaredUntrustedDerivation, in.DeclaredUntrustedNote)
			data.DeclaredUntrustedDerivation = in.DeclaredUntrustedDerivation
			data.DeclaredUntrustedNote = in.DeclaredUntrustedNote
		}
		return nil, newCreateChangeSetOutput(data), nil
	}))
}
