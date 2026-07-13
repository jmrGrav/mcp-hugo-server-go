package contentmodel

// TaxonomyTerm is the canonical agent-facing taxonomy identity used by the
// shared page model. It intentionally carries only stable identifier/display
// fields and does not depend on any other internal package.
type TaxonomyTerm struct {
	Source string `json:"source"`
	Slug   string `json:"slug"`
	Label  string `json:"label"`
}

// PageIdentity is the canonical cross-tool description of a page. It is a
// shared model only; individual handlers may still project it into legacy
// response DTOs while the wider contract migration is in progress.
//
// Do NOT add filesystem paths or internal keys here — if this type is ever
// serialized directly those fields would leak to agents. Use ResolvedSource
// for source-file location.
type PageIdentity struct {
	Slug        string         `json:"slug"`
	Lang        string         `json:"lang"`
	URL         string         `json:"url"`
	Title       string         `json:"title"`
	Tags        []TaxonomyTerm `json:"tags"`
	Categories  []TaxonomyTerm `json:"categories"`
	ReadingTime int            `json:"reading_time"`
}

// ResolvedSource identifies the concrete source file selected for a slug/lang
// lookup under a Hugo content root.
type ResolvedSource struct {
	Slug       string `json:"slug"`
	Lang       string `json:"lang"`
	SourcePath string `json:"source_path"`
}
