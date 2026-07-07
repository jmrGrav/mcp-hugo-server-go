package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/releasecheck"
)

func main() {
	version := flag.String("version", "", "release version to extract, for example v1.3.4")
	path := flag.String("changelog", "CHANGELOG.md", "path to changelog file")
	flag.Parse()

	if *version == "" {
		fmt.Fprintln(os.Stderr, "missing required -version")
		os.Exit(2)
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read changelog: %v\n", err)
		os.Exit(2)
	}
	notes, err := releasecheck.ExtractChangelogReleaseNotes(string(data), *version)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(notes)
}
