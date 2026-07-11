package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/releasecheck"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("extract-changelog-release", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "release version to extract, for example v1.3.4")
	path := fs.String("changelog", "CHANGELOG.md", "path to changelog file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version == "" {
		fmt.Fprintln(stderr, "missing required -version")
		return 2
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(stderr, "read changelog: %v\n", err)
		return 2
	}
	notes, err := releasecheck.ExtractChangelogReleaseNotes(string(data), *version)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, notes)
	return 0
}
