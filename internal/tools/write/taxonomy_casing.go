package write

import (
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/taxonomy"
)

// taxonomyCasingChangeDTO records one term create_page/update_page rewrote
// to match an existing casing already present in the index (#589), or left
// untouched because the index itself already has conflicting casings for
// that term (the #577 casing_variant scenario) — never guessed.
type taxonomyCasingChangeDTO struct {
	Type string `json:"type"` // "tag" or "category"
	From string `json:"from"`
	To   string `json:"to"`
}

// taxonomyCasingSkippedDTO records one term left exactly as typed because
// normalizeTaxonomyCasing found more than one distinct existing casing
// already in use for its slug+lang — the ambiguous case this feature
// deliberately refuses to guess at.
type taxonomyCasingSkippedDTO struct {
	Type string `json:"type"` // "tag" or "category"
	Term string `json:"term"`
}

// taxonomyRawForms indexes every distinct raw tag/category spelling
// currently in the site, grouped by normalized slug and language: forms[slug][lang]
// is the set of distinct raw strings seen for that slug in that language.
// Scoping by language mirrors the read-side casing_variant detection (#577)
// — a casing difference that only ever appears in different languages is
// left alone as a possible deliberate per-language style choice, not
// something to canonicalize across.
func taxonomyRawForms(idx *hugosite.SourceIndex, noun string) map[string]map[string]map[string]bool {
	out := map[string]map[string]map[string]bool{}
	if idx == nil {
		return out
	}
	for _, p := range idx.ListPages(0, 0) {
		terms := p.Tags
		if noun == "category" {
			terms = p.Categories
		}
		for _, t := range terms {
			s := taxonomy.Slug(t)
			if out[s] == nil {
				out[s] = map[string]map[string]bool{}
			}
			if out[s][p.Lang] == nil {
				out[s][p.Lang] = map[string]bool{}
			}
			out[s][p.Lang][t] = true
		}
	}
	return out
}

// normalizeTaxonomyCasing resolves each term in terms against the existing
// same-language raw casing already present in rawForms (#589):
//   - a term that already matches an existing form verbatim is left alone.
//   - a term whose slug+lang has exactly one existing distinct form adopts
//     that form's spelling — this is the actual normalization, preventing a
//     newly typed casing from drifting away from what the site already uses.
//   - a term whose slug+lang has zero existing forms is left as typed: it
//     becomes the new canonical spelling, there is nothing to normalize
//     against yet.
//   - a term whose slug+lang already has two or more distinct existing
//     forms (pre-existing drift) is left as typed and reported separately
//     as skipped: silently picking one of several already-coexisting
//     spellings would be a guess, not a normalization.
func normalizeTaxonomyCasing(rawForms map[string]map[string]map[string]bool, noun, lang string, terms []string) ([]string, []taxonomyCasingChangeDTO, []taxonomyCasingSkippedDTO) {
	if len(terms) == 0 {
		return terms, nil, nil
	}
	out := make([]string, len(terms))
	var changes []taxonomyCasingChangeDTO
	var skipped []taxonomyCasingSkippedDTO
	for i, term := range terms {
		out[i] = term
		forms := rawForms[taxonomy.Slug(term)][lang]
		if forms[term] {
			continue
		}
		switch len(forms) {
		case 0:
			// Nothing to normalize against yet.
		case 1:
			for existing := range forms {
				out[i] = existing
				changes = append(changes, taxonomyCasingChangeDTO{Type: noun, From: term, To: existing})
			}
		default:
			skipped = append(skipped, taxonomyCasingSkippedDTO{Type: noun, Term: term})
		}
	}
	return out, changes, skipped
}
