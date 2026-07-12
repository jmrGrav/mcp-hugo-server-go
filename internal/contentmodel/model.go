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
type PageIdentity struct {
	Slug           string         `json:"slug"`
	Lang           string         `json:"lang"`
	URL            string         `json:"url"`
	SourcePath     string         `json:"source_path"`
	TranslationKey string         `json:"translation_key,omitempty"`
	Title          string         `json:"title"`
	Tags           []TaxonomyTerm `json:"tags"`
	Categories     []TaxonomyTerm `json:"categories"`
	ReadingTime    int            `json:"reading_time"`
}

// ResolvedSource identifies the concrete source file selected for a slug/lang
// lookup under a Hugo content root.
type ResolvedSource struct {
	Slug       string `json:"slug"`
	Lang       string `json:"lang"`
	SourcePath string `json:"source_path"`
}
