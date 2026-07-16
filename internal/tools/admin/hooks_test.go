package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestHookRedirectRejected(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("redirect target must not be requested")
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	cfg := config.Config{PostBuildHooks: []string{redirector.URL}}
	results := fireHooks(context.Background(), cfg, newHookHTTPClient(5*time.Second))
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != 0 {
		t.Fatalf("redirected hook status = %d, want 0", results[0].Status)
	}
	if !strings.Contains(results[0].Error, "redirect") {
		t.Fatalf("redirected hook error = %q, want redirect detail", results[0].Error)
	}
}

func TestHookResponseTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxHookResponseBytes+1024))
	}))
	defer srv.Close()

	cfg := config.Config{PostBuildHooks: []string{srv.URL}}
	results := fireHooks(context.Background(), cfg, newHookHTTPClient(5*time.Second))
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Status != 0 {
		t.Fatalf("oversized hook status = %d, want 0", results[0].Status)
	}
	if !strings.Contains(results[0].Error, "response_too_large") {
		t.Fatalf("oversized hook error = %q, want response_too_large", results[0].Error)
	}
}
