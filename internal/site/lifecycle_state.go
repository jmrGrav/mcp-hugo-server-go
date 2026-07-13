package site

import (
	"os"
	"path/filepath"
	"strings"
)

// LifecycleState exposes which representation of a page the caller is seeing.
// The values are additive contract metadata for agents and operators.
type LifecycleState struct {
	SourceState string `json:"source_state"`
	BuildState  string `json:"build_state"`
	PublicState string `json:"public_state"`
	IndexState  string `json:"index_state"`
}

// StateForResolvedPage describes the representation returned by a read tool.
func StateForResolvedPage(resolved ResolvedPage, siteRoot string) LifecycleState {
	switch {
	case resolved.Public != nil:
		sourceState := "absent"
		buildState := "built"
		publicState := "available"
		indexState := "fresh"
		if resolved.Source != nil {
			sourceState = "present"
			if resolved.Source.BuildPending || sourceNewerThanPublicOutput(resolved, siteRoot) {
				buildState = "pending"
				publicState = "stale"
				indexState = "stale"
			}
		}
		return LifecycleState{
			SourceState: sourceState,
			BuildState:  buildState,
			PublicState: publicState,
			IndexState:  indexState,
		}
	case resolved.Source != nil:
		return LifecycleState{
			SourceState: "present",
			BuildState:  "pending",
			PublicState: "not_yet_available",
			IndexState:  "source_only",
		}
	default:
		return LifecycleState{}
	}
}

func sourceNewerThanPublicOutput(resolved ResolvedPage, siteRoot string) bool {
	if resolved.Public == nil || resolved.Source == nil || siteRoot == "" || resolved.Source.FilePath == "" {
		return false
	}
	sourceInfo, err := os.Stat(resolved.Source.FilePath)
	if err != nil {
		return false
	}
	publicPath := publicHTMLPath(siteRoot, resolved.Public.Slug)
	publicInfo, err := os.Stat(publicPath)
	if err != nil {
		return false
	}
	return sourceInfo.ModTime().After(publicInfo.ModTime())
}

func publicHTMLPath(siteRoot, slug string) string {
	trimmed := strings.Trim(slug, "/")
	if trimmed == "" {
		return filepath.Join(siteRoot, "index.html")
	}
	return filepath.Join(siteRoot, filepath.FromSlash(trimmed), "index.html")
}
