package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/releasecheck"
)

func main() {
	version := flag.String("version", "", "release version to verify, for example v1.2.10")
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
	if err := releasecheck.CheckChangelogVersion(string(data), *version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("CHANGELOG.md contains %s\n", normalizeForPrint(*version))
}

func normalizeForPrint(version string) string {
	if version == "" || version[0] == 'v' {
		return version
	}
	return "v" + version
}
