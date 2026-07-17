package admin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/fileutil"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// verifyPublicationHTTPTimeout bounds the single outbound GET this tool
// makes to confirm the public HTTP surface is actually serving the page —
// this is the one admin probe in this package that leaves the host, so it
// gets its own (slightly more generous) timeout than probeTimeout, which
// only bounds local subprocess calls.
const verifyPublicationHTTPTimeout = 10 * time.Second

// verifyPublicationMaxWaitSeconds bounds `wait_seconds` server-side (#421) so
// a caller can never turn this into a long-held connection — it smooths over
// the normal build-then-reindex lag immediately after a mutation, not a
// general-purpose long poll. A var, not a const, so tests can shrink it to
// avoid real multi-second sleeps.
var verifyPublicationMaxWaitSeconds = 20

// verifyPublicationPollInterval is how often the tool re-derives publication
// state while `wait_seconds` is active. A var so tests can shrink it.
var verifyPublicationPollInterval = 500 * time.Millisecond

var verifyPublicationClient = &http.Client{
	Timeout: verifyPublicationHTTPTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type verifyPublicationInput struct {
	Slug string `json:"slug"`
	// WaitSeconds, if set (#421), makes the tool poll internally instead of
	// checking once: it returns as soon as source/build/public/index all
	// agree ("fresh"), or once WaitSeconds elapses with whatever the state
	// is by then. Clamped server-side to verifyPublicationMaxWaitSeconds.
	// Omitted or 0 preserves the original single point-in-time check.
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

type verifyPublicationData struct {
	Slug           string `json:"slug"`
	URL            string `json:"url"`
	Source         string `json:"source"`
	Build          string `json:"build"`
	Public         string `json:"public"`
	Index          string `json:"index"`
	HTTPChecked    bool   `json:"http_checked"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	HTTPError      string `json:"http_error,omitempty"`
	SitemapPresent bool   `json:"sitemap_present"`
	FeedPresent    bool   `json:"feed_present"`
	Status         string `json:"status"`
	Explanation    string `json:"explanation"`
	// WaitSeconds echoes the actual (clamped) wait budget that was applied
	// (#421), so a caller that requested more than the server maximum can
	// see it was clamped. Omitted when no wait was requested.
	WaitSeconds int `json:"wait_seconds,omitempty"`
}

type verifyPublicationOutput struct {
	toolcontract.ToolResponse[verifyPublicationData]
}

// RegisterVerifyPublication wires verify_publication (site.admin scope). It
// is registered separately from admin.Register because, unlike every other
// tool in this package, it needs the public site index and (optionally) the
// source index to resolve a page's lifecycle state — the caller (server.go)
// only has srcIdx available conditionally, the same precondition already
// used for read.RegisterWithSourceIndex.
func RegisterVerifyPublication(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:  "verify_publication",
		Title: "Verify publication",
		Description: "Prove that a page's source, build, public HTML output, and in-memory index all correspond to the " +
			"same intended revision, and that the public HTTP surface is actually serving it. Answers 'is the public " +
			"site actually serving the intended change?' without requiring SSH access to the host. Optional " +
			"`wait_seconds` polls the local source/build/public/index state (bounded server-side to a small maximum, " +
			"echoed back in the response if your request was clamped) and returns as soon as it settles, instead of " +
			"always checking once and leaving you to poll it yourself across multiple calls; it only smooths a page " +
			"the index already knows about catching up after an edit — a brand-new page the index hasn't picked up " +
			"yet will not resolve mid-wait no matter how long you wait. Omit `wait_seconds` to preserve the original " +
			"single point-in-time check. Requires site.admin because it makes one outbound HTTP request to the " +
			"configured site_url.",
		InputSchema:  tools.MustSchema[verifyPublicationInput](),
		OutputSchema: tools.MustSchema[verifyPublicationOutput](),
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: fileutil.BoolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   fileutil.BoolPtr(true),
		},
	}, toolcontract.WrapTool(func(ctx context.Context, _ *mcp.CallToolRequest, in verifyPublicationInput) (*mcp.CallToolResult, verifyPublicationOutput, error) {
		if idx == nil {
			return nil, verifyPublicationOutput{}, fmt.Errorf("index not initialized")
		}
		slug := strings.TrimSpace(in.Slug)
		if slug == "" {
			return nil, verifyPublicationOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
		}

		data, err := pollForFreshPublication(ctx, idx, srcIdx, cfg, slug, in.WaitSeconds)
		if err != nil {
			return nil, verifyPublicationOutput{}, err
		}

		meta := toolcontract.NewMeta(buildinfo.Version, time.Now())
		return nil, verifyPublicationOutput{ToolResponse: toolcontract.Success(data, meta)}, nil
	}))
}

// pollForFreshPublication implements #421's bounded wait: with
// requestedWaitSeconds <= 0 it's a single checkPublicationOnce call,
// byte-for-byte the tool's pre-#421 behavior. With a positive value
// (clamped server-side to verifyPublicationMaxWaitSeconds), it re-derives
// local publication state (source/build/public/index — no network) on
// verifyPublicationPollInterval ticks until that local state settles or the
// wait budget is exhausted, then performs exactly one full
// checkPublicationOnce call (including the outbound HTTP probe, if
// applicable) to produce the returned data.
//
// The HTTP probe deliberately runs only once, at the end, not on every
// tick: verifyPublicationHTTPTimeout (10s) could otherwise make a "20s"
// wait overrun to ~30s wall-clock on a slow/unreachable host, and a full
// wait would fire dozens of GETs at the live site for no benefit — the
// thing #421 is actually waiting on is local build/reindex catching up
// with source, not network flakiness. data.WaitSeconds always echoes the
// clamped budget that was actually applied, so a caller who asked for more
// than the server maximum can see it was capped.
//
// Scope limit: idx is a snapshot built at server startup/last reindex, not
// a live filesystem view — resolver.Resolve consults it in-memory (see
// PageResolver.Resolve), so a page resolver.Resolve can't yet see at all
// (e.g. a brand-new page created after the snapshot) can never resolve to
// resolved.Public mid-poll no matter how long wait_seconds runs; only
// updates to a page the index already knows about get the live-disk-mtime
// benefit sourceNewerThanPublicOutput provides. wait_seconds smooths the
// build-catches-up-with-an-edit lag, not "wait for the index to notice a
// brand-new page."
func pollForFreshPublication(ctx context.Context, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, slug string, requestedWaitSeconds int) (verifyPublicationData, error) {
	waitSeconds := requestedWaitSeconds
	if waitSeconds > verifyPublicationMaxWaitSeconds {
		waitSeconds = verifyPublicationMaxWaitSeconds
	}
	if waitSeconds < 0 {
		waitSeconds = 0
	}

	if waitSeconds > 0 {
		deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	pollLoop:
		for {
			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.Resolve(slug)
			if !ok {
				return verifyPublicationData{}, fmt.Errorf("content_not_found: no source or public page found for slug %q", slug)
			}
			if localPublicationSettled(site.StateForResolvedPage(resolved, cfg.SiteRoot)) {
				break
			}
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			interval := verifyPublicationPollInterval
			if remaining < interval {
				interval = remaining
			}
			select {
			case <-ctx.Done():
				break pollLoop
			case <-time.After(interval):
			}
		}
	}

	data, err := checkPublicationOnce(ctx, idx, srcIdx, cfg, slug)
	if err != nil {
		return verifyPublicationData{}, err
	}
	data.WaitSeconds = waitSeconds
	return data, nil
}

// localPublicationSettled reports whether source/build/public/index agree,
// using only the local state StateForResolvedPage already derives from
// disk mtimes/presence — no network. It intentionally does not require the
// HTTP probe to have succeeded (that's checked once, separately, at the end
// of pollForFreshPublication): the wait budget is spent settling local
// build/reindex lag, not retrying a flaky network probe on every tick.
func localPublicationSettled(state site.LifecycleState) bool {
	if state.PublicState == "not_yet_available" {
		return false
	}
	return state.BuildState != "pending" && state.PublicState != "stale" && state.IndexState != "stale"
}

// checkPublicationOnce performs a single point-in-time publication-state
// check — the tool's original, unchanged behavior when wait_seconds is
// absent, and the unit the #421 poll loop repeats. It re-derives everything
// from live disk state (resolver.Resolve/StateForResolvedPage stat the
// source/public files directly) each call, so repeated calls do observe a
// build catching up mid-poll.
func checkPublicationOnce(ctx context.Context, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, slug string) (verifyPublicationData, error) {
	resolver := site.NewPageResolver(idx, srcIdx, cfg)
	resolved, ok := resolver.Resolve(slug)
	if !ok {
		return verifyPublicationData{}, fmt.Errorf("content_not_found: no source or public page found for slug %q", slug)
	}

	state := site.StateForResolvedPage(resolved, cfg.SiteRoot)

	data := verifyPublicationData{
		Slug:   slug,
		Source: state.SourceState,
		Build:  state.BuildState,
		Public: state.PublicState,
		Index:  state.IndexState,
	}
	// data.URL is always derived from cfg.SiteURL (operator-trusted
	// config) + the page's own slug — deliberately never taken from
	// resolved.Public.URL, which is copied out of the page's own
	// <link rel="canonical"> tag during indexing. A content.write actor
	// controls that tag; trusting it here would mean a lower-privileged
	// actor could steer this site.admin tool's outbound HTTP probe at an
	// arbitrary host (SSRF) and would also mean the check verifies the
	// wrong URL (the whole point is "does cfg.SiteURL serve this page").
	pageSlug := slug
	if resolved.Public != nil {
		pageSlug = resolved.Public.Slug
	} else if resolved.Source != nil {
		pageSlug = resolved.Source.Slug
	}
	data.Slug = pageSlug
	data.URL = joinSiteURL(cfg.SiteURL, pageSlug)

	data.SitemapPresent = publicationArtifactPresent(cfg.SiteRoot, "sitemap.xml")
	data.FeedPresent = publicationArtifactPresent(cfg.SiteRoot, "index.xml") || publicationArtifactPresent(cfg.SiteRoot, "feed.xml")

	if resolved.Public != nil && cfg.SiteURL != "" {
		status, httpErr := probePublicationURL(ctx, data.URL)
		data.HTTPChecked = true
		data.HTTPStatus = status
		data.HTTPError = httpErr
	}

	data.Status, data.Explanation = summarizePublicationState(data)
	return data, nil
}

func publicationArtifactPresent(siteRoot, name string) bool {
	if strings.TrimSpace(siteRoot) == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(siteRoot, name))
	return err == nil && !fi.IsDir()
}

func probePublicationURL(ctx context.Context, url string) (status int, errText string) {
	tctx, cancel := context.WithTimeout(ctx, verifyPublicationHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(tctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "request could not be constructed"
	}
	resp, err := verifyPublicationClient.Do(req)
	if err != nil {
		return 0, "request failed: the public URL may be unreachable from this host"
	}
	defer resp.Body.Close()
	return resp.StatusCode, ""
}

func joinSiteURL(siteURL, slug string) string {
	siteURL = strings.TrimRight(strings.TrimSpace(siteURL), "/")
	slug = strings.TrimSpace(slug)
	if siteURL == "" {
		return slug
	}
	if !strings.HasPrefix(slug, "/") {
		slug = "/" + slug
	}
	return siteURL + slug
}

// summarizePublicationState derives an overall status and a short
// human-readable explanation of which stage, if any, is stale or missing —
// the acceptance criterion "it can explain which stage is stale or missing."
func summarizePublicationState(data verifyPublicationData) (status, explanation string) {
	switch {
	case data.Public == "not_yet_available":
		return "not_yet_published", "source exists but has not been built/published yet (build_state=" + data.Build + ")"
	case data.Build == "pending" || data.Public == "stale" || data.Index == "stale":
		return "stale", "source is newer than the public build output (build_state=" + data.Build + ", public_state=" + data.Public + ", index_state=" + data.Index + ")"
	case data.HTTPChecked && data.HTTPError != "":
		return "http_unreachable", data.HTTPError
	case data.HTTPChecked && data.HTTPStatus >= 400:
		return "http_error", fmt.Sprintf("public URL responded with HTTP %d", data.HTTPStatus)
	default:
		return "fresh", "source, build, public output, and index all agree on the same revision"
	}
}
