package read_test

import (
	"sort"
	"testing"
)

// TestCrossToolTaxonomyConsistency verifies that every MCP tool that emits
// taxonomy data for the same page returns identical tag_terms/category_terms
// (same slugs and labels). This is the closure criterion for issue #175.
//
// The bonjour page is used because it exists only in the public index (no source
// override) and carries mixed-case tags from HTML meta ("Hugo", "Security"),
// giving the test meaningful normalization coverage.
func TestCrossToolTaxonomyConsistency(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	const slug = "/posts/bonjour/"

	// Helper: extract tag_terms slugs from a page map.
	extractTagSlugs := func(t *testing.T, tool string, page map[string]any) []string {
		t.Helper()
		raw, ok := page["tag_terms"]
		if !ok {
			t.Errorf("%s: missing tag_terms", tool)
			return nil
		}
		items, ok := raw.([]any)
		if !ok {
			t.Errorf("%s: tag_terms is %T, want []any", tool, raw)
			return nil
		}
		var slugs []string
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := m["slug"].(string); ok {
				slugs = append(slugs, s)
			}
		}
		sort.Strings(slugs)
		return slugs
	}

	extractLabels := func(t *testing.T, tool string, page map[string]any) map[string]string {
		t.Helper()
		raw, ok := page["tag_terms"]
		if !ok {
			return nil
		}
		items, _ := raw.([]any)
		labels := make(map[string]string)
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			slug, _ := m["slug"].(string)
			label, _ := m["label"].(string)
			if slug != "" {
				labels[slug] = label
			}
		}
		return labels
	}

	// Collect results from each tool.
	type toolResult struct {
		tool   string
		slugs  []string
		labels map[string]string
	}
	var results []toolResult

	// get_full_page_markdown
	{
		res := callTool(t, session, "get_full_page_markdown", map[string]any{"slug": slug})
		if res.IsError {
			t.Fatalf("get_full_page_markdown error")
		}
		m := decodeContent(t, res)
		page, _ := m["page"].(map[string]any)
		results = append(results, toolResult{
			tool:   "get_full_page_markdown",
			slugs:  extractTagSlugs(t, "get_full_page_markdown", page),
			labels: extractLabels(t, "get_full_page_markdown", page),
		})
	}

	// get_page_frontmatter
	{
		res := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": slug})
		if res.IsError {
			t.Fatalf("get_page_frontmatter error")
		}
		m := decodeContent(t, res)
		fm, _ := m["frontmatter"].(map[string]any)
		results = append(results, toolResult{
			tool:   "get_page_frontmatter",
			slugs:  extractTagSlugs(t, "get_page_frontmatter", fm),
			labels: extractLabels(t, "get_page_frontmatter", fm),
		})
	}

	// search_content — find the bonjour page by tag slug.
	// search_content wraps its output in a {"data": {"pages": [...]}} envelope.
	{
		res := callTool(t, session, "search_content", map[string]any{"tag": "security", "limit": 5})
		if res.IsError {
			t.Fatalf("search_content error")
		}
		m := decodeContent(t, res)
		data, _ := m["data"].(map[string]any)
		pages, _ := data["pages"].([]any)
		var found map[string]any
		for _, p := range pages {
			pm, _ := p.(map[string]any)
			if pm["slug"] == slug {
				found = pm
				break
			}
		}
		if found == nil {
			t.Fatalf("search_content with tag=security did not return %s (slug-based filter broken)", slug)
		}
		results = append(results, toolResult{
			tool:   "search_content",
			slugs:  extractTagSlugs(t, "search_content", found),
			labels: extractLabels(t, "search_content", found),
		})
	}

	// build_agent_context
	{
		res := callTool(t, session, "build_agent_context", map[string]any{"slug": slug})
		if res.IsError {
			t.Fatalf("build_agent_context error")
		}
		m := decodeContent(t, res)
		ctx, _ := m["context"].(map[string]any)
		fm, _ := ctx["frontmatter"].(map[string]any)
		results = append(results, toolResult{
			tool:   "build_agent_context",
			slugs:  extractTagSlugs(t, "build_agent_context", fm),
			labels: extractLabels(t, "build_agent_context", fm),
		})
	}

	// export_agent_context with tag filter — verifies slug-based filtering
	{
		res := callTool(t, session, "export_agent_context", map[string]any{"tag": "Security", "limit": 5})
		if res.IsError {
			t.Fatalf("export_agent_context error")
		}
		m := decodeContent(t, res)
		export, _ := m["export"].(map[string]any)
		exportPages, _ := export["pages"].([]any)
		var found map[string]any
		for _, p := range exportPages {
			pm, _ := p.(map[string]any)
			fm, _ := pm["frontmatter"].(map[string]any)
			if fm["slug"] == slug {
				found = fm
				break
			}
		}
		if found == nil {
			// The export filter uses slug-based matching; "Security" → slug "security"
			// should match the page tagged with "Security" in its HTML.
			t.Fatalf("export_agent_context tag=Security did not return %s (slug filter broken)", slug)
		}
		results = append(results, toolResult{
			tool:   "export_agent_context",
			slugs:  extractTagSlugs(t, "export_agent_context", found),
			labels: extractLabels(t, "export_agent_context", found),
		})
	}

	// Assert all tools agree on the same tag slugs and labels.
	if len(results) < 2 {
		t.Fatal("not enough tool results to compare")
	}
	ref := results[0]
	for _, r := range results[1:] {
		if !stringSliceEqual(ref.slugs, r.slugs) {
			t.Errorf("tag_terms slug mismatch:\n  %s: %v\n  %s: %v",
				ref.tool, ref.slugs, r.tool, r.slugs)
		}
		for slug, refLabel := range ref.labels {
			if got := r.labels[slug]; got != refLabel {
				t.Errorf("label mismatch for slug %q: %s=%q, %s=%q",
					slug, ref.tool, refLabel, r.tool, got)
			}
		}
	}

	// Also verify expected normalization: "Hugo" and "Security" in HTML → correct slugs.
	ref = results[0]
	wantSlugs := []string{"hugo", "security"}
	sort.Strings(wantSlugs)
	if !stringSliceEqual(ref.slugs, wantSlugs) {
		t.Errorf("expected tag slugs %v, got %v (normalization broken)", wantSlugs, ref.slugs)
	}
	if ref.labels["hugo"] != "Hugo" {
		t.Errorf("label for 'hugo' = %q, want 'Hugo'", ref.labels["hugo"])
	}
	if ref.labels["security"] != "Security" {
		t.Errorf("label for 'security' = %q, want 'Security'", ref.labels["security"])
	}
}

// TestCrossToolTaxonomySourceOverride verifies that all authenticated read tools
// agree on taxonomy for a page whose source frontmatter differs from its public HTML.
// /posts/hello/ has source tags [go, hugo] but HTML meta tags [Hugo, Read-only].
// Every authenticated tool must use the source, so all must agree on slugs {go, hugo}.
func TestCrossToolTaxonomySourceOverride(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	const slug = "/posts/hello/"

	extractTagSlugs := func(t *testing.T, tool string, page map[string]any) []string {
		t.Helper()
		raw, ok := page["tag_terms"]
		if !ok {
			t.Errorf("%s: missing tag_terms", tool)
			return nil
		}
		items, _ := raw.([]any)
		var slugs []string
		for _, item := range items {
			m, _ := item.(map[string]any)
			if s, ok := m["slug"].(string); ok {
				slugs = append(slugs, s)
			}
		}
		sort.Strings(slugs)
		return slugs
	}

	type toolResult struct {
		tool  string
		slugs []string
	}
	var results []toolResult

	// get_full_page_markdown
	{
		res := callTool(t, session, "get_full_page_markdown", map[string]any{"slug": slug})
		if res.IsError {
			t.Fatalf("get_full_page_markdown error")
		}
		m := decodeContent(t, res)
		page, _ := m["page"].(map[string]any)
		results = append(results, toolResult{"get_full_page_markdown", extractTagSlugs(t, "get_full_page_markdown", page)})
	}

	// get_page_frontmatter
	{
		res := callTool(t, session, "get_page_frontmatter", map[string]any{"slug": slug})
		if res.IsError {
			t.Fatalf("get_page_frontmatter error")
		}
		m := decodeContent(t, res)
		fm, _ := m["frontmatter"].(map[string]any)
		results = append(results, toolResult{"get_page_frontmatter", extractTagSlugs(t, "get_page_frontmatter", fm)})
	}

	// build_agent_context
	{
		res := callTool(t, session, "build_agent_context", map[string]any{"slug": slug})
		if res.IsError {
			t.Fatalf("build_agent_context error")
		}
		m := decodeContent(t, res)
		ctx, _ := m["context"].(map[string]any)
		fm, _ := ctx["frontmatter"].(map[string]any)
		results = append(results, toolResult{"build_agent_context", extractTagSlugs(t, "build_agent_context", fm)})
	}

	// export_agent_context — filter by tag=Hugo (slug "hugo") to find the page via the
	// public index, then verify the result shows source tags (go + hugo), not HTML tags.
	{
		res := callTool(t, session, "export_agent_context", map[string]any{"tag": "Hugo", "limit": 10})
		if res.IsError {
			t.Fatalf("export_agent_context error")
		}
		m := decodeContent(t, res)
		export, _ := m["export"].(map[string]any)
		exportPages, _ := export["pages"].([]any)
		var found map[string]any
		for _, p := range exportPages {
			pm, _ := p.(map[string]any)
			fm, _ := pm["frontmatter"].(map[string]any)
			if fm["slug"] == slug {
				found = fm
				break
			}
		}
		if found == nil {
			t.Fatalf("export_agent_context tag=Hugo did not return %s (slug-based filter broken)", slug)
		}
		results = append(results, toolResult{"export_agent_context", extractTagSlugs(t, "export_agent_context", found)})
	}

	// All authenticated tools must agree on the source tags: go + hugo.
	wantSlugs := []string{"go", "hugo"}
	sort.Strings(wantSlugs)
	for _, r := range results {
		if !stringSliceEqual(r.slugs, wantSlugs) {
			t.Errorf("%s tag_terms mismatch: got %v, want %v", r.tool, r.slugs, wantSlugs)
		}
	}
	if len(results) >= 2 {
		ref := results[0]
		for _, r := range results[1:] {
			if !stringSliceEqual(ref.slugs, r.slugs) {
				t.Errorf("cross-tool divergence:\n  %s: %v\n  %s: %v",
					ref.tool, ref.slugs, r.tool, r.slugs)
			}
		}
	}
}

// TestComputeRelatedSlugMatching verifies that computeRelated uses slug-based
// matching, so "Hugo" and "hugo" are treated as the same tag across pages.
func TestComputeRelatedSlugMatching(t *testing.T) {
	idx := mustTestIndex(t)
	session, done := newTestClient(t, idx)
	defer done()

	// posts/hello has HTML tags "Hugo", "Read-only"
	// posts/bonjour has HTML tags "Hugo", "Security"
	// They share "Hugo" (case may differ between source and HTML), so they should be related.
	res := callTool(t, session, "get_related_content", map[string]any{"slug": "/posts/hello/"})
	if res.IsError {
		t.Fatalf("get_related_content error")
	}
	m := decodeContent(t, res)
	related, _ := m["related"].([]any)

	var bonjourFound bool
	for _, r := range related {
		rm, _ := r.(map[string]any)
		if rm["slug"] == "/posts/bonjour/" {
			bonjourFound = true
			// Verify shared_tag_terms are present
			if terms, ok := rm["shared_tag_terms"]; ok {
				termsSlice, _ := terms.([]any)
				if len(termsSlice) == 0 {
					t.Error("get_related_content: shared_tag_terms present but empty")
				}
				// Check that the shared term has the expected structure
				first, _ := termsSlice[0].(map[string]any)
				if first["slug"] == nil || first["label"] == nil || first["source"] == nil {
					t.Errorf("shared_tag_terms entry missing fields: %v", first)
				}
			} else {
				t.Error("get_related_content: missing shared_tag_terms field")
			}
		}
	}
	if !bonjourFound {
		slugNames := make([]string, 0, len(related))
		for _, r := range related {
			rm, _ := r.(map[string]any)
			slugNames = append(slugNames, rm["slug"].(string))
		}
		t.Errorf("expected /posts/bonjour/ in related pages for /posts/hello/ (slug-based matching), got: %v", slugNames)
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

