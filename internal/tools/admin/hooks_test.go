package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHookAllowlisted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{PostBuildHooks: []string{srv.URL}}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	RegisterHooks(s, cfg)

	results := fireHooks(context.Background(), cfg, hookClient)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != http.StatusOK {
		t.Errorf("expected status 200, got %d", results[0].Status)
	}
	if results[0].Error != "" {
		t.Errorf("expected no error, got %q", results[0].Error)
	}
}

func TestHookNotAllowlisted(t *testing.T) {
	cfg := config.Config{PostBuildHooks: []string{}}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	RegisterHooks(s, cfg)

	results := fireHooks(context.Background(), cfg, hookClient)
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty hook list (SSRF protection), got %d", len(results))
	}
}

func TestHookTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{PostBuildHooks: []string{srv.URL}}
	shortClient := &http.Client{Timeout: 100 * time.Millisecond}

	results := fireHooks(context.Background(), cfg, shortClient)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != 0 {
		t.Errorf("expected status 0 on timeout, got %d", results[0].Status)
	}
	if results[0].Error == "" {
		t.Error("expected non-empty error on timeout")
	}
}
