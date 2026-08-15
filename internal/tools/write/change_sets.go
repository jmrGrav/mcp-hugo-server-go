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
		id, err := changeSets.Create(principalID, time.Now().UTC())
		if err != nil {
			return nil, createChangeSetOutput{}, err
		}
		return nil, newCreateChangeSetOutput(createChangeSetData{ChangeSetID: id}), nil
	}))
}
