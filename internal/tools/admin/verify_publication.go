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

var verifyPublicationClient = &http.Client{
	Timeout: verifyPublicationHTTPTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type verifyPublicationInput struct {
	Slug string `json:"slug"`
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
			"site actually serving the intended change?' without requiring SSH access to the host. Requires site.admin " +
			"because it makes one outbound HTTP request to the configured site_url.",
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

		resolver := site.NewPageResolver(idx, srcIdx, cfg)
		resolved, ok := resolver.Resolve(slug)
		if !ok {
			return nil, verifyPublicationOutput{}, fmt.Errorf("content_not_found: no source or public page found for slug %q", slug)
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

		meta := toolcontract.NewMeta(buildinfo.Version, time.Now())
		return nil, verifyPublicationOutput{ToolResponse: toolcontract.Success(data, meta)}, nil
	}))
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
