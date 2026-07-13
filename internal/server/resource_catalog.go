package server

import (
	"context"
	"encoding/json"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/contentmodel"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerSharedResources(s *mcp.Server) {
	if s == nil {
		return
	}
	addSchemaResource[contentmodel.PageIdentity](s,
		"schema://mcp-hugo-server-go/contentmodel/page-identity",
		"page-identity",
		"Page Identity Schema",
		"Canonical shared schema for page identity reused across read and write tool responses.")
	addSchemaResource[toolcontract.PaginationMeta](s,
		"schema://mcp-hugo-server-go/toolcontract/pagination-meta",
		"pagination-meta",
		"Pagination Metadata Schema",
		"Shared schema for self-descriptive pagination fields such as total, offset, has_more, and next_offset.")
	addSchemaResource[site.LifecycleState](s,
		"schema://mcp-hugo-server-go/site/lifecycle-state",
		"lifecycle-state",
		"Lifecycle State Schema",
		"Shared schema for source/build/public/index lifecycle state exposed by page read and mutation tools.")
}

func addSchemaResource[T any](s *mcp.Server, uri, name, title, description string) {
	schemaBytes, err := json.MarshalIndent(tools.MustSchema[T](), "", "  ")
	if err != nil {
		panic(err)
	}
	s.AddResource(&mcp.Resource{
		URI:         uri,
		Name:        name,
		Title:       title,
		Description: description,
		MIMEType:    "application/schema+json",
		Size:        int64(len(schemaBytes)),
	}, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/schema+json",
			Text:     string(schemaBytes),
		}}}, nil
	})
}
