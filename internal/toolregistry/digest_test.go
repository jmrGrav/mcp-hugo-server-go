package toolregistry_test

import (
	"context"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolregistry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type digestInput struct{}
type digestOutput struct {
	Value string `json:"value"`
}

func addTool(t *testing.T, s *mcp.Server, name, description string) {
	t.Helper()
	mcp.AddTool(s, &mcp.Tool{Name: name, Description: description},
		func(context.Context, *mcp.CallToolRequest, digestInput) (*mcp.CallToolResult, digestOutput, error) {
			return nil, digestOutput{Value: "ok"}, nil
		})
}

func newServerWithTools(t *testing.T, names ...string) *mcp.Server {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	for _, n := range names {
		addTool(t, s, n, "description for "+n)
	}
	return s
}

// TestDigestIsDeterministicAcrossToolRegistrationOrder pins the property the
// whole feature depends on: two servers with the identical tool set
// registered in different orders must produce the identical digest — the
// digest fingerprints the published contract, not registration order.
func TestDigestIsDeterministicAcrossToolRegistrationOrder(t *testing.T) {
	s1 := newServerWithTools(t, "alpha", "beta", "gamma")
	s2 := newServerWithTools(t, "gamma", "alpha", "beta")

	snap1, err := toolregistry.FromServer(context.Background(), s1)
	if err != nil {
		t.Fatalf("FromServer(s1) error = %v", err)
	}
	snap2, err := toolregistry.FromServer(context.Background(), s2)
	if err != nil {
		t.Fatalf("FromServer(s2) error = %v", err)
	}

	d1, err := toolregistry.Digest(snap1)
	if err != nil {
		t.Fatalf("Digest(snap1) error = %v", err)
	}
	d2, err := toolregistry.Digest(snap2)
	if err != nil {
		t.Fatalf("Digest(snap2) error = %v", err)
	}
	if d1 != d2 {
		t.Fatalf("digest depends on registration order: %q vs %q", d1, d2)
	}
	if d1 == "" {
		t.Fatal("digest is empty")
	}
}

// TestDigestChangesWhenDescriptionChanges is the actual tool-poisoning /
// rug-pull detection property: editing an existing tool's description text
// alone (no name/count change) must move the digest, unlike #1175's
// tool_names_revision which only tracks the name set.
func TestDigestChangesWhenDescriptionChanges(t *testing.T) {
	sBefore := newServerWithTools(t, "alpha")
	sAfter := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	addTool(t, sAfter, "alpha", "a completely different, rewritten description")

	before, err := toolregistry.FromServer(context.Background(), sBefore)
	if err != nil {
		t.Fatalf("FromServer(before) error = %v", err)
	}
	after, err := toolregistry.FromServer(context.Background(), sAfter)
	if err != nil {
		t.Fatalf("FromServer(after) error = %v", err)
	}

	dBefore, err := toolregistry.Digest(before)
	if err != nil {
		t.Fatalf("Digest(before) error = %v", err)
	}
	dAfter, err := toolregistry.Digest(after)
	if err != nil {
		t.Fatalf("Digest(after) error = %v", err)
	}
	if dBefore == dAfter {
		t.Fatalf("digest unchanged after a description-only edit: both %q", dBefore)
	}
}

// TestDigestStableWhenNothingChanges guards against accidental
// nondeterminism (e.g. an unsorted map somewhere upstream) across repeated
// snapshots of the exact same server.
func TestDigestStableWhenNothingChanges(t *testing.T) {
	s := newServerWithTools(t, "alpha", "beta")
	snap, err := toolregistry.FromServer(context.Background(), s)
	if err != nil {
		t.Fatalf("FromServer error = %v", err)
	}
	d1, err := toolregistry.Digest(snap)
	if err != nil {
		t.Fatalf("Digest error = %v", err)
	}
	d2, err := toolregistry.Digest(snap)
	if err != nil {
		t.Fatalf("Digest error = %v", err)
	}
	if d1 != d2 {
		t.Fatalf("Digest is not stable across repeated calls on the same snapshot: %q vs %q", d1, d2)
	}
}
