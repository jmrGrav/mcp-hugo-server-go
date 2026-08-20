package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/diffcoverage"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check-diff-coverage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profilePath := fs.String("profile", "coverage.out", "path to a go test -coverprofile output file")
	diffPath := fs.String("diff", "", "path to a `git diff --unified=0` output file (required)")
	module := fs.String("module", "github.com/jmrGrav/mcp-hugo-server-go", "Go module path prefix to strip from profile file entries")
	min := fs.Float64("min", 85, "minimum diff-coverage percentage required")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *diffPath == "" {
		fmt.Fprintln(stderr, "check-diff-coverage: -diff is required")
		return 2
	}

	profileFile, err := os.Open(*profilePath)
	if err != nil {
		fmt.Fprintf(stderr, "open profile: %v\n", err)
		return 2
	}
	defer profileFile.Close()
	blocks, err := diffcoverage.ParseProfile(profileFile)
	if err != nil {
		fmt.Fprintf(stderr, "parse profile: %v\n", err)
		return 2
	}

	diffBytes, err := os.ReadFile(*diffPath)
	if err != nil {
		fmt.Fprintf(stderr, "read diff: %v\n", err)
		return 2
	}
	changed := diffcoverage.ChangedLines(string(diffBytes))

	prefix := strings.TrimSuffix(*module, "/") + "/"
	result := diffcoverage.Compute(blocks, changed, func(profileFile string) string {
		return strings.TrimPrefix(profileFile, prefix)
	})

	if result.TotalStmts == 0 {
		fmt.Fprintln(stdout, "check-diff-coverage: no coverable statements changed, skipping")
		return 0
	}

	pct := result.Percent()
	fmt.Fprintf(stdout, "Diff coverage: %.1f%% (%d/%d statements)\n", pct, result.CoveredStmts, result.TotalStmts)
	if len(result.Uncovered) > 0 {
		sort.Slice(result.Uncovered, func(i, j int) bool {
			if result.Uncovered[i].File != result.Uncovered[j].File {
				return result.Uncovered[i].File < result.Uncovered[j].File
			}
			return result.Uncovered[i].StartLine < result.Uncovered[j].StartLine
		})
		fmt.Fprintln(stdout, "Uncovered changed statements:")
		for _, b := range result.Uncovered {
			fmt.Fprintf(stdout, "  %s:%d-%d\n", b.File, b.StartLine, b.EndLine)
		}
	}

	if pct < *min {
		fmt.Fprintf(stderr, "check-diff-coverage: %.1f%% is below the required %.1f%% for changed lines\n", pct, *min)
		return 1
	}
	return 0
}
