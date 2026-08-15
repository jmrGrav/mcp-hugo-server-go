package buildinfo

import (
	"runtime/debug"
	"strings"
)

// SchemaVersion tracks the response contract shape. Additive fields increment
// the minor version; incompatible changes increment the major version.
//
// v2.0.0 (#1118): check_sri_versions/run_post_build_hooks/preview_build's
// root-level payload aliases (deprecated since #1060) are removed — data.*
// is now the sole payload location for every structured tool, finishing the
// convergence #520/#573 started for the other 7.
const SchemaVersion = "v2.0.0"

// Version is set at build time via -ldflags.
var Version = "dev"

// ReleaseVersion is the human-facing product/release identifier (e.g.
// v1.5.5) when known at build time. Production deploys pass this explicitly
// via deploy.yml's release_version input at deploy time, ahead of the git
// tag itself (which release.yml only creates once the deployment is live
// and verified) — see docs/operator-guide.md's "Production Deploy +
// Release" section. Empty means "this build has no known release identity
// yet"; callers can still inspect Version/Commit in that case.
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
	applyBuildInfo(info, ok)
}

func applyBuildInfo(info *debug.BuildInfo, ok bool) {
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
