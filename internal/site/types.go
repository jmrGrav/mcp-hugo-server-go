package site

type Page struct {
	Slug       string
	Title      string
	Summary    string
	Tags       []string
	Categories []string
	Date       string
	URL        string
	Lang       string
	RawHTML    string
	// OutputPath is the rendered HTML file's path relative to cfg.SiteRoot
	// (forward-slash separated), e.g. "en/posts/foo/index.html". Populated
	// during index construction from the same file walk that produced
	// RawHTML; empty for synthetic/zero-value Pages.
	OutputPath string
}
