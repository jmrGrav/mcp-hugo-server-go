package previewstore

import "regexp"

// MalformedLanguagePrefixPattern matches the reversed, invalid preview URL
// ordering "/{lang}/preview/{id}/..." instead of the canonical
// "/preview/{id}/{lang}/...". Capture group 1 is the language segment,
// group 2 is the preview id. Both the create_preview fix-up pass and the
// inspect_preview_rendered detection check must share this single pattern
// so the two never drift apart (#996).
var MalformedLanguagePrefixPattern = regexp.MustCompile(`/([A-Za-z]{2,8}(?:-[A-Za-z0-9]+)?)/preview/([a-f0-9]+)/`)
