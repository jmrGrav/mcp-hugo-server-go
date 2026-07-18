package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 9 {
		t.Fatalf("Defs() = %d, want 9", len(defs))
	}
	if defs[0].RequiredScope != "write" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}

func TestFetchImageErrorBranches(t *testing.T) {
	if _, err := fetchImage(context.Background(), "://bad-url", "", "prompt"); err == nil {
		t.Fatal("fetchImage() should fail on malformed URL")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := fetchImage(context.Background(), srv.URL, "", "prompt"); err == nil {
		t.Fatal("fetchImage() should fail on non-2xx response")
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, config.Default())
	RegisterSiteAdmin(nil, config.Default())
	RegisterBuild(nil, config.Default())
	RegisterPreviewBuild(nil, config.Default())
	RegisterHooks(nil, config.Default())
	RegisterSRI(nil, config.Default())
}
