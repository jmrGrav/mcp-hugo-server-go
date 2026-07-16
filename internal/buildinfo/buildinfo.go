package buildinfo

import "runtime/debug"

const SchemaVersion = "v1.0.0"

// Version is set at build time via -ldflags.
var Version = "dev"

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
