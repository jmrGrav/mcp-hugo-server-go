package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/releasecheck"
)

func main() {
	path := flag.String("readme", "README.md", "path to README file")
	flag.Parse()

	data, err := os.ReadFile(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read README: %v\n", err)
		os.Exit(2)
	}
	if err := releasecheck.CheckReadmeReleasePolicy(string(data)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("README.md release metadata is dynamic")
}
