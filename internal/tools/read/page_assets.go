package read

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listPageAssetsInput struct {
	Slug string `json:"slug"`
}

// pageAssetDTO describes one sibling file stored alongside a page bundle's
// index.md (e.g. an image referenced by the page). #348: complements
// upload_page_asset (content.write) by letting agents discover what already
// exists in a bundle before writing a new asset.
type pageAssetDTO struct {
	Name       string `json:"name"`
	SizeBytes  int64  `json:"size_bytes"`
	ModifiedAt string `json:"modified_at"`
}

type listPageAssetsData struct {
	Assets []pageAssetDTO `json:"assets"`
}

type listPageAssetsOutput struct {
	toolcontract.ToolResponse[listPageAssetsData]
	Assets []pageAssetDTO `json:"assets"`
}

func newListPageAssetsOutput(data listPageAssetsData, now time.Time) listPageAssetsOutput {
	return listPageAssetsOutput{ToolResponse: successEnvelope(data, now), Assets: data.Assets}
}

// RegisterListPageAssets registers list_page_assets. Separate function
// (mirrors RegisterListContentTypes/RegisterInspectRenderedPage) since it
// needs its own PageResolver per call, not shared registration state.
func RegisterListPageAssets(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "list_page_assets", "List page assets",
		"List the sibling files (images, etc.) stored alongside a Hugo page bundle's index.md, e.g. cover.webp next to content/posts/article/index.md. Only leaf page bundles have an asset directory; single-file pages (slug.md, no per-page directory) fail with not_a_bundle. Use before upload_page_asset to check what already exists. The on-disk bundle directory is a source-derived signal unavailable to the reader profile even when the page itself is public; readers receive an empty assets list instead of an error. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPageAssetsInput) (*mcp.CallToolResult, listPageAssetsOutput, error) {
			slug := strings.Trim(strings.TrimSpace(in.Slug), "/")
			if slug == "" {
				return nil, listPageAssetsOutput{}, fmt.Errorf("invalid_params: slug must not be empty")
			}
			resolver := site.NewPageResolver(idx, srcIdx, cfg)
			resolved, ok := resolver.Resolve(slug)
			if !ok {
				return nil, listPageAssetsOutput{}, fmt.Errorf("content_not_found: page not found for slug %q", slug)
			}
			resolved, err := readerSafeResolvedPage(ctx, resolved, slug)
			if err != nil {
				return nil, listPageAssetsOutput{}, err
			}
			if resolved.SourcePath == "" {
				// Reader profile: the source bundle directory is intentionally
				// unavailable (see site.ReaderSafeResolvedPage), so there is no
				// path to list. Return an empty list rather than an error, same
				// omission-not-block treatment as #324/#339's source-derived
				// fields.
				return nil, newListPageAssetsOutput(listPageAssetsData{Assets: []pageAssetDTO{}}, time.Now().UTC()), nil
			}

			base := filepath.Base(resolved.SourcePath)
			if !strings.HasPrefix(base, "index.") {
				return nil, listPageAssetsOutput{}, fmt.Errorf("not_a_bundle: slug %q is a single-file page with no bundle directory for assets", slug)
			}
			dir := filepath.Dir(resolved.SourcePath)
			entries, err := os.ReadDir(dir)
			if err != nil {
				slog.Error("list_page_assets: read dir failed", "slug", slug, "error", err)
				return nil, listPageAssetsOutput{}, fmt.Errorf("read_error: failed to list page bundle directory")
			}

			assets := make([]pageAssetDTO, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if strings.HasPrefix(name, "index.") && strings.HasSuffix(name, ".md") {
					continue
				}
				if strings.HasPrefix(name, ".") {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				assets = append(assets, pageAssetDTO{
					Name:       name,
					SizeBytes:  info.Size(),
					ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
				})
			}
			sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })

			return nil, newListPageAssetsOutput(listPageAssetsData{Assets: assets}, time.Now().UTC()), nil
		})
}
