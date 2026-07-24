package write

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mutationStatusLookupTools is the vocabulary get_mutation_status accepts
// for its `tool` input — deliberately the exact set that shares the single
// idempotencyStore instance passed into Register, not every write tool
// (create_preview/generate_hero_image/etc. don't take idempotency_key).
var mutationStatusLookupTools = map[string]bool{
	"create_page":       true,
	"update_page":       true,
	"delete_page":       true,
	"upload_page_asset": true,
	"delete_page_asset": true,
}

type getMutationStatusInput struct {
	Tool           string `json:"tool"`
	IdempotencyKey string `json:"idempotency_key"`
}

// getMutationStatusData's Result is the exact success payload the original
// mutation call would have returned (or did return, to whichever caller
// completed it) — same shape as that tool's own data.*, passed through
// verbatim rather than re-typed here, so this stays correct automatically
// as each tool's own output shape evolves. Typed as map[string]any (every
// mutation tool's success payload is a JSON object) rather than
// json.RawMessage: the schema generator has no way to know a []byte-backed
// json.RawMessage is actually an arbitrary object, and emits an array
// schema for it, which then fails output validation on every real result.
type getMutationStatusData struct {
	Tool           string         `json:"tool"`
	IdempotencyKey string         `json:"idempotency_key"`
	Status         string         `json:"status"`
	Result         map[string]any `json:"result,omitempty"`
}

type getMutationStatusOutput struct {
	toolcontract.ToolResponse[getMutationStatusData]
}

func newGetMutationStatusOutput(data getMutationStatusData) getMutationStatusOutput {
	return getMutationStatusOutput{ToolResponse: writeSuccessEnvelope(data)}
}

// registerGetMutationStatus wires get_mutation_status (#586): a read-only
// way to ask "did my last create_page/update_page/... actually land" after
// a timeout or otherwise ambiguous response, using the same idempotency_key
// that call was (or would have been) made with — without resending the
// original mutation payload, and without guessing from list_pages/get_page.
// Deliberately requires write scope: it exposes the same success payload a
// write-scoped caller already received once, at the same trust level.
func registerGetMutationStatus(s *mcp.Server, idem *idempotencyStore) {
	// The retention window is a server-configured deployment knob
	// (config.Config.IdempotencyTTLSeconds, #616), not fixed at 15 minutes
	// anymore — describe the actual configured value so this description
	// never goes stale relative to the running server's config.
	ttlDesc := "15 minutes"
	if idem != nil && idem.ttl > 0 {
		ttlDesc = formatTTLDescription(idem.ttl)
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_mutation_status",
		Title: "Get mutation status",
		Description: "Look up whether a prior create_page/update_page/delete_page/upload_page_asset/delete_page_asset call " +
			"that used idempotency_key actually succeeded — for recovering from a timeout or otherwise ambiguous response " +
			"without resending the original mutation payload. `status: \"succeeded\"` means that exact call completed and " +
			"`result` is its entire original response envelope (success/data/errors/warnings/meta, not just data), byte-identical " +
			"to what a same-key/same-payload retry of the mutation tool itself would replay. `status: \"unknown\"` means there is no confirmed success on record for this tool+key " +
			"right now — this does NOT mean the call failed: it equally covers still-in-flight, genuinely failed, expired " +
			"(entries are retained " + ttlDesc + " on this server — a deployment-level setting, shared with the underlying idempotency cache), or never attempted with this key. Only successful calls are ever recorded here " +
			"(failures are safe to simply retry). When in doubt, retrying the original mutation call with the same " +
			"idempotency_key and payload is always safe regardless of what this tool reports — it will either replay the " +
			"already-completed result or execute for the first time. Requires content.write.",
		InputSchema:  tools.MustSchema[getMutationStatusInput](),
		OutputSchema: tools.MustSchema[getMutationStatusOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(false),
		},
	}, toolcontract.WrapTool(func(_ context.Context, _ *mcp.CallToolRequest, in getMutationStatusInput) (*mcp.CallToolResult, getMutationStatusOutput, error) {
		if !mutationStatusLookupTools[in.Tool] {
			return nil, getMutationStatusOutput{}, fmt.Errorf("invalid_params: tool must be one of create_page, update_page, delete_page, upload_page_asset, delete_page_asset")
		}
		if in.IdempotencyKey == "" {
			return nil, getMutationStatusOutput{}, fmt.Errorf("invalid_params: idempotency_key must not be empty")
		}
		raw, found := idem.lookup(in.Tool, in.IdempotencyKey)
		status := "unknown"
		var result map[string]any
		if found {
			status = "succeeded"
			if err := json.Unmarshal(raw, &result); err != nil {
				return nil, getMutationStatusOutput{}, fmt.Errorf("internal_error: failed to decode recorded mutation result")
			}
		}
		return nil, newGetMutationStatusOutput(getMutationStatusData{
			Tool:           in.Tool,
			IdempotencyKey: in.IdempotencyKey,
			Status:         status,
			Result:         result,
		}), nil
	}))
}
