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
// SourcePath and TranslationKey are intentional agent-visible fields:
// #271 requires every tool to return a consistent source_path, and
// #273 requires translation_key for separating translations from related pages.
// Both are populated progressively as handlers migrate to this type.
type PageIdentity struct {
	Slug           string         `json:"slug"`
	SourceKey      string         `json:"source_key,omitempty"`
	Lang           string         `json:"lang"`
	URL            string         `json:"url"`
	SourcePath     string         `json:"source_path"`
	Revision       string         `json:"revision,omitempty"`
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
