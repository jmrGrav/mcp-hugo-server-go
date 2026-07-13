package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterSharedResourcesPublishesSchemas(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	registerSharedResources(s)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer session.Close()

	resources, err := session.ListResources(ctx, nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources.Resources) < 3 {
		t.Fatalf("ListResources() returned %d resources, want at least 3 shared schemas", len(resources.Resources))
	}

	want := map[string]bool{
		"schema://mcp-hugo-server-go/contentmodel/page-identity":   false,
		"schema://mcp-hugo-server-go/toolcontract/pagination-meta": false,
		"schema://mcp-hugo-server-go/site/lifecycle-state":         false,
	}
	for _, r := range resources.Resources {
		if _, ok := want[r.URI]; ok {
			want[r.URI] = true
		}
	}
	for uri, seen := range want {
		if !seen {
			t.Fatalf("missing shared resource %q in resources/list", uri)
		}
	}

	readRes, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "schema://mcp-hugo-server-go/contentmodel/page-identity"})
	if err != nil {
		t.Fatalf("ReadResource(page-identity): %v", err)
	}
	if len(readRes.Contents) != 1 {
		t.Fatalf("ReadResource(page-identity) returned %d contents, want 1", len(readRes.Contents))
	}
	content := readRes.Contents[0]
	if content.MIMEType != "application/schema+json" {
		t.Fatalf("page-identity mime = %q, want application/schema+json", content.MIMEType)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(content.Text), &schema); err != nil {
		t.Fatalf("page-identity schema is not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("page-identity schema type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("page-identity properties type = %T", schema["properties"])
	}
	if _, ok := props["slug"]; !ok {
		t.Fatalf("page-identity schema missing slug property: %#v", props)
	}
}
