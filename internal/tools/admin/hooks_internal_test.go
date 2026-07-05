package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestFireHooks(t *testing.T) {
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.PostBuildHooks = []string{srv.URL}
	results := fireHooks(context.Background(), cfg, srv.Client())
	if len(results) != 1 || results[0].Status != http.StatusNoContent || seen != 1 {
		t.Fatalf("fireHooks() = %#v seen=%d", results, seen)
	}
}

func TestFireHookError(t *testing.T) {
	res := fireHook(context.Background(), http.DefaultClient, "http://127.0.0.1:1", []byte("{}"))
	if res.Error == "" {
		t.Fatal("fireHook() should surface connection error")
	}
}

func TestRegisterHooksNilServer(t *testing.T) {
	RegisterHooks(nil, config.Config{})
}
