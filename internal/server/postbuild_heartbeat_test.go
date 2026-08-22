package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
)

// TestPostBuildCallbacksHeartbeatPingsOnSuccess verifies the "heartbeat"
// PostBuildCallback's Fn (build success path) contacts every configured
// heartbeat_hooks URL unsuffixed. The ping runs in a detached goroutine
// (see pingHeartbeatAsync's doc comment for why), so this test waits on a
// channel rather than asserting synchronously after Fn returns.
func TestPostBuildCallbacksHeartbeatPingsOnSuccess(t *testing.T) {
	pinged := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pinged <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.HeartbeatHooks = []string{srv.URL}

	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}

	callback := findPostBuildCallback(t, postBuildCallbacks("build_site", slog.Default(), cfg, idx, nil, nil), "heartbeat")
	if err := callback.Fn(); err != nil {
		t.Fatalf("heartbeat Fn() error = %v", err)
	}

	select {
	case path := <-pinged:
		if path != "/" {
			t.Errorf("success ping path = %q, want / (unsuffixed)", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for heartbeat success ping")
	}
}

// TestPostBuildCallbacksHeartbeatPingsFailSuffixOnBuildFailed verifies the
// "heartbeat" PostBuildCallback's OnBuildFailed appends the BetterStack
// /fail suffix, and — critically — that the call returns immediately rather
// than blocking on the network request. notifyBuildFailed (build.go) runs
// this synchronously while ContentMu is still held during a real build; a
// blocking implementation here would stall every other MCP mutation for as
// long as the heartbeat endpoint takes to respond.
func TestPostBuildCallbacksHeartbeatPingsFailSuffixOnBuildFailed(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	pinged := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived) // proves the request is in flight before we check OnBuildFailed
		<-release      // held open until the test explicitly releases it
		pinged <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.HeartbeatHooks = []string{srv.URL}

	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("site.NewIndex() error = %v", err)
	}

	callback := findPostBuildCallback(t, postBuildCallbacks("build_site", slog.Default(), cfg, idx, nil, nil), "heartbeat")

	returned := make(chan error, 1)
	go func() {
		returned <- callback.OnBuildFailed(admin.BuildProgress{}, "failed:build_error")
	}()

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("OnBuildFailed() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnBuildFailed blocked on the network call instead of returning immediately")
	}

	// The request must still be in flight (handler blocked on <-release) at
	// this point — proving OnBuildFailed above returned WHILE the network
	// call was still outstanding, not merely quickly. Without this, the
	// test would pass identically for a synchronous-but-fast implementation.
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the heartbeat request to reach the server")
	}

	close(release)

	select {
	case path := <-pinged:
		if path != "/fail" {
			t.Errorf("fail ping path = %q, want /fail", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for heartbeat fail ping")
	}
}
