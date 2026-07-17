package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
)

// withShortPollTuning shrinks verifyPublicationMaxWaitSeconds and
// verifyPublicationPollInterval for the duration of a test, so #421's
// bounded-wait tests don't have to sleep the real 20s production maximum.
func withShortPollTuning(t *testing.T, maxWaitSeconds int, pollInterval time.Duration) {
	t.Helper()
	origMax, origInterval := verifyPublicationMaxWaitSeconds, verifyPublicationPollInterval
	verifyPublicationMaxWaitSeconds = maxWaitSeconds
	verifyPublicationPollInterval = pollInterval
	t.Cleanup(func() {
		verifyPublicationMaxWaitSeconds = origMax
		verifyPublicationPollInterval = origInterval
	})
}

func writeStalePublicationFixture(t *testing.T) (cfg config.Config, srcIdx *hugosite.SourceIndex, idx *site.Index, publicPath string) {
	t.Helper()
	contentRoot := t.TempDir()
	siteRoot := t.TempDir()
	pagePath := filepath.Join(contentRoot, "posts", "hello", "index.md")
	if err := os.MkdirAll(filepath.Dir(pagePath), 0o755); err != nil {
		t.Fatalf("mkdir content: %v", err)
	}
	if err := os.WriteFile(pagePath, []byte("---\ntitle: Hello\ndate: 2026-07-14\n---\nUpdated body.\n"), 0o644); err != nil {
		t.Fatalf("write page: %v", err)
	}
	publicPath = filepath.Join(siteRoot, "posts", "hello", "index.html")
	if err := os.MkdirAll(filepath.Dir(publicPath), 0o755); err != nil {
		t.Fatalf("mkdir public: %v", err)
	}
	if err := os.WriteFile(publicPath, []byte(`<!DOCTYPE html><html><head><title>Hello</title></head><body>Stale.</body></html>`), 0o644); err != nil {
		t.Fatalf("write public: %v", err)
	}
	// Source is unambiguously newer than public output — starts "stale".
	longAgo := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(publicPath, longAgo, longAgo); err != nil {
		t.Fatalf("chtimes public output: %v", err)
	}

	var err error
	srcIdx, err = hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}

	cfg = config.Default()
	cfg.SiteRoot = siteRoot
	// SiteURL deliberately blank: skips the outbound HTTP probe, which is
	// irrelevant to these poll-timing tests and would otherwise make
	// "fresh" depend on network reachability instead of just source/public
	// mtimes (site.StateForResolvedPage) — see summarizePublicationState.
	cfg.SiteURL = ""
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000

	idx, err = site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}
	return cfg, srcIdx, idx, publicPath
}

// TestPollForFreshPublicationImmediateMatchReturnsFast covers #421: when
// the state is already "fresh" on the first check, the poll loop must not
// sleep at all, even with a large wait budget.
func TestPollForFreshPublicationImmediateMatchReturnsFast(t *testing.T) {
	withShortPollTuning(t, 20, 5*time.Second) // deliberately long interval/max: a real poll would blow the deadline of this test
	siteRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(siteRoot, "posts", "hello"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteRoot, "posts", "hello", "index.html"), []byte(`<!DOCTYPE html><html><head><title>Hello</title></head><body>Fresh.</body></html>`), 0o644); err != nil {
		t.Fatalf("write public: %v", err)
	}
	cfg := config.Default()
	cfg.SiteRoot = siteRoot
	cfg.SiteURL = "" // skip the HTTP probe, irrelevant to this test
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	start := time.Now()
	data, err := pollForFreshPublication(context.Background(), idx, nil, cfg, "posts/hello", 10)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("pollForFreshPublication() error = %v", err)
	}
	if data.Status != "fresh" {
		t.Fatalf("status = %q, want fresh", data.Status)
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %v, want well under 1s (already fresh on first check, must not sleep)", elapsed)
	}
	if data.WaitSeconds != 10 {
		t.Fatalf("WaitSeconds = %d, want 10 (echoes the requested budget even though it wasn't needed)", data.WaitSeconds)
	}
}

// TestPollForFreshPublicationEventualMatchReturnsEarly covers #421: a build
// catching up mid-poll (source no longer newer than public output, observed
// live via os.Stat — see sourceNewerThanPublicOutput) must be detected and
// returned as soon as it happens, not only after the full wait budget.
func TestPollForFreshPublicationEventualMatchReturnsEarly(t *testing.T) {
	pollInterval := 50 * time.Millisecond
	withShortPollTuning(t, 5, pollInterval)
	cfg, srcIdx, idx, publicPath := writeStalePublicationFixture(t)

	first, err := pollForFreshPublication(context.Background(), idx, srcIdx, cfg, "posts/hello", 0)
	if err != nil {
		t.Fatalf("pollForFreshPublication(wait=0) error = %v", err)
	}
	if first.Status != "stale" {
		t.Fatalf("precondition failed: status = %q, want stale before the fixture's simulated build catch-up", first.Status)
	}

	catchUpAfter := 150 * time.Millisecond
	go func() {
		time.Sleep(catchUpAfter)
		now := time.Now()
		_ = os.Chtimes(publicPath, now, now) // simulates the build rewriting public output after source changed
	}()

	start := time.Now()
	data, err := pollForFreshPublication(context.Background(), idx, srcIdx, cfg, "posts/hello", 5)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("pollForFreshPublication() error = %v", err)
	}
	if data.Status != "fresh" {
		t.Fatalf("status = %q, want fresh (build caught up mid-poll)", data.Status)
	}
	if elapsed >= 5*time.Second {
		t.Fatalf("elapsed = %v, want well under the 5s wait budget (must return as soon as fresh, not always wait the full duration)", elapsed)
	}
	if elapsed < catchUpAfter {
		t.Fatalf("elapsed = %v, want >= %v (the catch-up genuinely happened mid-poll, not before the first check)", elapsed, catchUpAfter)
	}
}

// TestPollForFreshPublicationTimesOutWithPartialState covers #421: if the
// state never reaches "fresh" within the wait budget, the tool must return
// once the budget is exhausted with whatever state it has — not hang
// indefinitely and not error out.
func TestPollForFreshPublicationTimesOutWithPartialState(t *testing.T) {
	waitSeconds := 1
	withShortPollTuning(t, waitSeconds, 50*time.Millisecond)
	cfg, srcIdx, idx, _ := writeStalePublicationFixture(t)
	// No goroutine touches publicPath this time — state stays "stale" forever.

	start := time.Now()
	data, err := pollForFreshPublication(context.Background(), idx, srcIdx, cfg, "posts/hello", waitSeconds)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("pollForFreshPublication() error = %v", err)
	}
	if data.Status != "stale" {
		t.Fatalf("status = %q, want stale (state never reached fresh within the wait budget)", data.Status)
	}
	if elapsed < time.Duration(waitSeconds)*time.Second {
		t.Fatalf("elapsed = %v, want >= %ds (must actually wait out the budget before giving up)", elapsed, waitSeconds)
	}
	if elapsed > time.Duration(waitSeconds)*time.Second+time.Second {
		t.Fatalf("elapsed = %v, want close to %ds (must not overrun the wait budget by much)", elapsed, waitSeconds)
	}
}

// TestPollForFreshPublicationClampsWaitSecondsToServerMaximum covers #421's
// acceptance criterion that a caller requesting more than the server
// maximum gets clamped, both in the echoed WaitSeconds and in actual
// elapsed time (state never reaches fresh, so elapsed time bounds the
// effective wait that was really applied).
func TestPollForFreshPublicationClampsWaitSecondsToServerMaximum(t *testing.T) {
	const serverMax = 1
	withShortPollTuning(t, serverMax, 50*time.Millisecond)
	cfg, srcIdx, idx, _ := writeStalePublicationFixture(t)

	start := time.Now()
	data, err := pollForFreshPublication(context.Background(), idx, srcIdx, cfg, "posts/hello", 999)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("pollForFreshPublication() error = %v", err)
	}
	if data.WaitSeconds != serverMax {
		t.Fatalf("WaitSeconds = %d, want %d (clamped to the server maximum, not the requested 999)", data.WaitSeconds, serverMax)
	}
	if elapsed > time.Duration(serverMax)*time.Second+time.Second {
		t.Fatalf("elapsed = %v, want bounded near the clamped %ds maximum, not anywhere close to the requested 999s", elapsed, serverMax)
	}
}

// TestPollForFreshPublicationZeroWaitIsUnchangedSingleCheck covers #421's
// acceptance criterion that omitting wait_seconds preserves the original
// single point-in-time check — no sleeping, no polling loop entered at all.
func TestPollForFreshPublicationZeroWaitIsUnchangedSingleCheck(t *testing.T) {
	withShortPollTuning(t, 20, 5*time.Second) // a real poll would blow this test's deadline
	cfg, srcIdx, idx, _ := writeStalePublicationFixture(t)

	start := time.Now()
	data, err := pollForFreshPublication(context.Background(), idx, srcIdx, cfg, "posts/hello", 0)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("pollForFreshPublication() error = %v", err)
	}
	if data.Status != "stale" {
		t.Fatalf("status = %q, want stale (single check, no polling)", data.Status)
	}
	if data.WaitSeconds != 0 {
		t.Fatalf("WaitSeconds = %d, want 0 (omitted/unset)", data.WaitSeconds)
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %v, want well under 1s (wait_seconds=0 must not enter the poll loop at all)", elapsed)
	}
}

// TestPollForFreshPublicationProbesHTTPOnlyOnce covers the self-review fix
// on #421: the poll loop must re-derive local (disk-only) state on every
// tick, but must only make the outbound HTTP probe once, at the end — not
// once per tick. Probing every tick would let verifyPublicationHTTPTimeout
// (10s) push a "20s" wait to ~30s wall-clock on a slow host, and would fire
// dozens of GETs at the live site for no benefit.
func TestPollForFreshPublicationProbesHTTPOnlyOnce(t *testing.T) {
	var probeCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	pollInterval := 30 * time.Millisecond
	withShortPollTuning(t, 5, pollInterval)
	cfg, srcIdx, idx, publicPath := writeStalePublicationFixture(t)
	cfg.SiteURL = upstream.URL

	catchUpAfter := 200 * time.Millisecond
	go func() {
		time.Sleep(catchUpAfter)
		now := time.Now()
		_ = os.Chtimes(publicPath, now, now)
	}()

	data, err := pollForFreshPublication(context.Background(), idx, srcIdx, cfg, "posts/hello", 5)
	if err != nil {
		t.Fatalf("pollForFreshPublication() error = %v", err)
	}
	if data.Status != "fresh" {
		t.Fatalf("status = %q, want fresh", data.Status)
	}
	if !data.HTTPChecked {
		t.Fatal("HTTPChecked = false, want true (the final check must still probe HTTP)")
	}
	// catchUpAfter / pollInterval ticks would have elapsed if every tick
	// probed HTTP; exactly 1 is the only correct count regardless of how
	// many ticks the local-only polling took.
	if got := probeCount.Load(); got != 1 {
		t.Fatalf("HTTP probe count = %d, want exactly 1 (local state polling must not re-probe HTTP on every tick)", got)
	}
}

// TestPollForFreshPublicationDoesNotResolveAPageTheIndexNeverSaw documents
// #421's scope limit: idx is a snapshot, not a live filesystem view (see
// PageResolver.Resolve), so a page that never existed in idx at all cannot
// be found no matter how long wait_seconds runs — this must fail fast with
// content_not_found, not hang out the full wait budget.
func TestPollForFreshPublicationDoesNotResolveAPageTheIndexNeverSaw(t *testing.T) {
	withShortPollTuning(t, 5, 30*time.Millisecond)
	cfg := config.Default()
	cfg.SiteRoot = t.TempDir()
	cfg.SiteURL = ""
	cfg.DefaultLanguage = "en"
	cfg.MaxIndexEntries = 1000
	idx, err := site.NewIndex(cfg)
	if err != nil {
		t.Fatalf("NewIndex() error = %v", err)
	}

	start := time.Now()
	_, err = pollForFreshPublication(context.Background(), idx, nil, cfg, "posts/never-existed", 5)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("pollForFreshPublication() error = nil, want content_not_found")
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %v, want fast failure, not spinning out the wait budget for a slug the index never had", elapsed)
	}
}
