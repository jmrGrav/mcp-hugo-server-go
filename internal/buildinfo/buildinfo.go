package buildinfo

import (
	"runtime/debug"
	"strings"
)

const SchemaVersion = "v1.0.0"

// Version is set at build time via -ldflags.
var Version = "dev"

// ReleaseVersion is the human-facing product/release identifier (e.g.
// v1.5.1) when known at build time. Empty means "this build is not tied to
// a named release"; callers can still inspect Version/Commit. Mainline
// production deploys intentionally use this empty state today: they expose
// Version like "main-<sha>" plus Commit/BuildChannel instead of inventing a
// release alias that was never cut from a tag.
var ReleaseVersion = ""

// BuildChannel identifies the deployment line the binary came from when it
// is known at build time (e.g. release, main, staging). Empty falls back to
// a deterministic derivation from Version.
var BuildChannel = ""

// Commit, CommitTime, and Dirty are populated automatically from Go's
// embedded VCS build info (available whenever the binary was built with
// `go build`/`go install` from inside a Git checkout — no -ldflags needed).
// They stay at their zero value for `go run`, binaries built outside a Git
// checkout, or Go toolchains that don't embed VCS metadata.
var (
	Commit     string
	CommitTime string
	Dirty      bool
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			Commit = s.Value
		case "vcs.time":
			CommitTime = s.Value
		case "vcs.modified":
			Dirty = s.Value == "true"
		}
	}
}

func EffectiveReleaseVersion() string {
	if v := strings.TrimSpace(ReleaseVersion); v != "" {
		return v
	}
	v := strings.TrimSpace(Version)
	if strings.HasPrefix(v, "v") {
		return v
	}
	return ""
}

func EffectiveBuildChannel() string {
	if ch := strings.TrimSpace(BuildChannel); ch != "" {
		return ch
	}
	if EffectiveReleaseVersion() != "" && strings.TrimSpace(Version) == EffectiveReleaseVersion() {
		return "release"
	}
	if head, _, ok := strings.Cut(strings.TrimSpace(Version), "-"); ok && head != "" {
		return head
	}
	if v := strings.TrimSpace(Version); v != "" {
		return v
	}
	return "dev"
}
