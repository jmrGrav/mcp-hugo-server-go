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
	fs := flag.NewFlagSet("check-readme-release", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("readme", "README.md", "path to README file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	data, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(stderr, "read README: %v\n", err)
		return 2
	}
	if err := releasecheck.CheckReadmeReleasePolicy(string(data)); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "README.md release metadata is dynamic")
	return 0
}
