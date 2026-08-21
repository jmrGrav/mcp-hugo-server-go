// Package toolregistry computes a stable, client-verifiable fingerprint of an
// *mcp.Server's published tool surface (#1225). The purpose is tool-poisoning
// / rug-pull detection: an MCP client that trusts a server's tool set at
// connect time has no built-in way to notice a later deployment silently
// changing a tool's description or input schema (the "rug pull" attack this
// issue is named for — a tool that looked safe when approved is edited after
// the fact to embed different instructions). This package gives a client a
// value to pin on first use and compare on every reconnect.
//
// The digest is deliberately per-deployment, not a single universal value
// pinned to a release: newScopedServer conditionally registers write/admin
// tools only when writeEnabled, and internal/server.ScopeExtension lets an
// embedder register arbitrary additional tools. A read-only deployment or one
// with extensions legitimately produces a different digest than the
// repository's own checked-in golden — that is expected trust-on-first-use
// behavior per deployment, not evidence of tampering.
package toolregistry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolSnapshot is one tool's client-visible contract surface: exactly what a
// real MCP client sees from tools/list, not the raw registration struct.
// RequiredScope is deliberately excluded — it is not part of the published
// tools/list surface a client actually observes, and callers that need it can
// join it separately (as the contract-snapshot golden test does).
type ToolSnapshot struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	InputSchema  any    `json:"input_schema"`
	OutputSchema any    `json:"output_schema"`
}

// FromServer connects an in-memory client/server pair to s, lists every tool
// s currently publishes, and returns the snapshots sorted by name. This
// mirrors exactly what a real MCP client receives from tools/list — the same
// mechanism internal/contracttests uses for its registry golden snapshot —
// so the digest reflects the actual published contract, not an
// internal registration list that could silently diverge from it.
func FromServer(ctx context.Context, s *mcp.Server) ([]ToolSnapshot, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		return nil, fmt.Errorf("toolregistry: server connect: %w", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "toolregistry-digest", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		return nil, fmt.Errorf("toolregistry: client connect: %w", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return nil, fmt.Errorf("toolregistry: ListTools: %w", err)
	}

	snapshots := make([]ToolSnapshot, 0, len(res.Tools))
	for _, tl := range res.Tools {
		snapshots = append(snapshots, ToolSnapshot{
			Name:         tl.Name,
			Description:  tl.Description,
			InputSchema:  tl.InputSchema,
			OutputSchema: tl.OutputSchema,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].Name < snapshots[j].Name })
	return snapshots, nil
}

// Digest returns a "sha256:<hex>" fingerprint of snapshots, computed over
// their canonical JSON encoding. Snapshots must already be sorted by name
// (FromServer guarantees this) — Digest does not re-sort, so callers building
// snapshots by another path are responsible for deterministic ordering.
func Digest(snapshots []ToolSnapshot) (string, error) {
	raw, err := json.Marshal(snapshots)
	if err != nil {
		return "", fmt.Errorf("toolregistry: marshal snapshots: %w", err)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
