package write

import (
	"sort"
	"strings"
)

// This file exposes the write-tool enforcement limits as exported accessors
// so a capability-discovery surface (get_capabilities, #859) can report the
// exact values the tools actually enforce, from one source of truth, instead
// of re-declaring them and risking drift. These deliberately return the
// unexported package constants verbatim — do not fork the values here.

// BodyMaxBytes is the maximum create_page/update_page body size (#590/#380).
func BodyMaxBytes() int { return maxBodyBytes }

// TitleMaxRunes is the maximum create_page/update_page title length.
func TitleMaxRunes() int { return maxTitleRunes }

// AssetMaxBytes is the maximum decoded upload_page_asset size.
func AssetMaxBytes() int { return maxAssetBytes }

// TestContentMaxTTLHours is the ceiling on an opt-in test_content TTL (#661).
func TestContentMaxTTLHours() int { return testContentMaxTTLHours }

// AllowedAssetExtensions returns the upload_page_asset image extensions
// (without the leading dot), sorted for deterministic output.
func AllowedAssetExtensions() []string {
	exts := make([]string, 0, len(allowedAssetTypes))
	for ext := range allowedAssetTypes {
		exts = append(exts, strings.TrimPrefix(ext, "."))
	}
	sort.Strings(exts)
	return exts
}
