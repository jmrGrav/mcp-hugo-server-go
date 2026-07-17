package read

import (
	"context"
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

// contentTypeDTO describes one Hugo content type/section: what archetype
// template (if any) governs new pages of this type, the union of front
// matter fields declared by that archetype and observed on existing pages
// of this type, and how many source pages of this type currently exist.
// #347: this lets an agent discover site-specific content conventions
// instead of guessing them from create_page trial and error.
type contentTypeDTO struct {
	Name           string   `json:"name"`
	Source         string   `json:"source"` // "archetype", "observed", or "archetype+observed"
	ArchetypePath  string   `json:"archetype_path,omitempty"`
	ExpectedFields []string `json:"expected_fields,omitempty"`
	PageCount      int      `json:"page_count,omitempty"`
}

type listContentTypesData struct {
	ContentTypes []contentTypeDTO `json:"content_types"`
}

type listContentTypesOutput struct {
	toolcontract.ToolResponse[listContentTypesData]
	ContentTypes []contentTypeDTO `json:"content_types"`
}

func newListContentTypesOutput(data listContentTypesData, now time.Time) listContentTypesOutput {
	return listContentTypesOutput{ToolResponse: successEnvelope(data, now), ContentTypes: data.ContentTypes}
}

// RegisterListContentTypes registers list_content_types. Separate function
// (mirrors RegisterInspectRenderedPage/RegisterDiffPage) since it needs
// cfg.HugoRoot for archetype discovery, not just idx/srcIdx.
func RegisterListContentTypes(s *mcp.Server, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	if s == nil {
		return
	}
	addReadOnlyTool(s, "list_content_types", "List content types",
		"Discover the site's Hugo content types/sections: which archetype template (if any) governs new pages of each type, what front matter fields are expected (union of the archetype's declared fields and fields observed on existing pages of that type), and how many source pages of each type currently exist. Use this before create_page on an unfamiliar site instead of guessing section/front matter conventions. `page_count` and observed-page-derived fields are omitted for the reader profile; archetype-derived fields (site configuration, not page content) remain visible. Requires content.read.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listContentTypesOutput, error) {
			archetypes := discoverArchetypes(cfg.HugoRoot)
			observed := map[string]int{}
			observedFields := map[string]map[string]struct{}{}
			qSrc := sourceIndexForProfile(srcIdx, site.IsReaderProfile(ctx))
			if qSrc != nil {
				for _, p := range qSrc.ListPages(0, 0) {
					name := firstSlugSegment(p.Slug)
					if name == "" {
						continue
					}
					observed[name]++
					fields := observedFields[name]
					if fields == nil {
						fields = map[string]struct{}{}
						observedFields[name] = fields
					}
					for k := range p.FrontmatterRaw {
						fields[k] = struct{}{}
					}
				}
			}

			names := make(map[string]struct{}, len(archetypes)+len(observed))
			for name := range archetypes {
				names[name] = struct{}{}
			}
			for name := range observed {
				names[name] = struct{}{}
			}
			out := make([]contentTypeDTO, 0, len(names))
			for name := range names {
				dto := contentTypeDTO{Name: name}
				arch, hasArch := archetypes[name]
				count, hasObserved := observed[name]
				switch {
				case hasArch && hasObserved:
					dto.Source = "archetype+observed"
				case hasArch:
					dto.Source = "archetype"
				default:
					dto.Source = "observed"
				}

				fieldSet := map[string]struct{}{}
				if hasArch {
					dto.ArchetypePath = arch.path
					for _, f := range arch.fields {
						fieldSet[f] = struct{}{}
					}
				}
				if hasObserved {
					dto.PageCount = count
					for f := range observedFields[name] {
						fieldSet[f] = struct{}{}
					}
				}
				if len(fieldSet) > 0 {
					fields := make([]string, 0, len(fieldSet))
					for f := range fieldSet {
						fields = append(fields, f)
					}
					sort.Strings(fields)
					dto.ExpectedFields = fields
				}
				out = append(out, dto)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

			return nil, newListContentTypesOutput(listContentTypesData{ContentTypes: out}, time.Now().UTC()), nil
		})
}

type archetypeInfo struct {
	path   string
	fields []string
}

// discoverArchetypes scans {hugoRoot}/archetypes/*.md for Hugo archetype
// templates, keyed by content type name (filename without extension).
// "default" is Hugo's fallback archetype applied when no type-specific one
// exists — it isn't itself a content type, so it's excluded from results.
func discoverArchetypes(hugoRoot string) map[string]archetypeInfo {
	out := map[string]archetypeInfo{}
	if hugoRoot == "" {
		return out
	}
	dir := filepath.Join(hugoRoot, "archetypes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "default" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fm, err := hugosite.ParseFrontmatterFile(path)
		if err != nil {
			continue
		}
		fields := make([]string, 0, len(fm))
		for k := range fm {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		out[name] = archetypeInfo{path: path, fields: fields}
	}
	return out
}

func firstSlugSegment(slug string) string {
	slug = strings.Trim(slug, "/")
	if slug == "" {
		return ""
	}
	parts := strings.SplitN(slug, "/", 2)
	return parts[0]
}
