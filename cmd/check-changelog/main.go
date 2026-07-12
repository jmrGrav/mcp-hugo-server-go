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
	fs := flag.NewFlagSet("check-changelog", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.String("version", "", "release version to verify, for example v1.2.10")
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
	if err := releasecheck.CheckChangelogVersion(string(data), *version); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "CHANGELOG.md contains %s\n", normalizeForPrint(*version))
	return 0
}

func normalizeForPrint(version string) string {
	if version == "" || version[0] == 'v' {
		return version
	}
	return "v" + version
}
