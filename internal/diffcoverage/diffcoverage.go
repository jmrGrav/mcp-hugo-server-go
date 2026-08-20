// Package diffcoverage measures test coverage restricted to the lines a
// change actually touches, distinct from the repo's aggregate coverage gate
// (which measures every line in ./internal/... regardless of whether a given
// PR modified it). It exists so a PR cannot dilute the aggregate 85% floor by
// adding untested code that happens to be offset by unrelated, already-well-
// covered files elsewhere in the tree.
package diffcoverage

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Block is one statement block from a `go test -coverprofile` profile.
type Block struct {
	File      string
	StartLine int
	EndLine   int
	NumStmt   int
	Count     int
}

// ParseProfile reads a Go coverage profile (the "mode: ..." header followed
// by "file:startLine.startCol,endLine.endCol numStmt count" lines).
func ParseProfile(r io.Reader) ([]Block, error) {
	var blocks []Block
	scanner := bufio.NewScanner(r)
	// Coverage profiles can be large; grow the buffer beyond bufio's 64KiB
	// default line limit rather than truncating a long path silently.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if lineNo == 1 && strings.HasPrefix(line, "mode:") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		b, err := parseProfileLine(line)
		if err != nil {
			return nil, fmt.Errorf("profile line %d: %w", lineNo, err)
		}
		blocks = append(blocks, b)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return blocks, nil
}

func parseProfileLine(line string) (Block, error) {
	// file:startLine.startCol,endLine.endCol numStmt count
	colonIdx := strings.LastIndex(line, ":")
	if colonIdx < 0 {
		return Block{}, fmt.Errorf("missing ':' in %q", line)
	}
	file := line[:colonIdx]
	rest := strings.Fields(line[colonIdx+1:])
	if len(rest) != 3 {
		return Block{}, fmt.Errorf("unexpected field count in %q", line)
	}
	posParts := strings.SplitN(rest[0], ",", 2)
	if len(posParts) != 2 {
		return Block{}, fmt.Errorf("unexpected position field in %q", line)
	}
	startLine, err := strconv.Atoi(strings.SplitN(posParts[0], ".", 2)[0])
	if err != nil {
		return Block{}, fmt.Errorf("bad start line in %q: %w", line, err)
	}
	endLine, err := strconv.Atoi(strings.SplitN(posParts[1], ".", 2)[0])
	if err != nil {
		return Block{}, fmt.Errorf("bad end line in %q: %w", line, err)
	}
	numStmt, err := strconv.Atoi(rest[1])
	if err != nil {
		return Block{}, fmt.Errorf("bad numStmt in %q: %w", line, err)
	}
	count, err := strconv.Atoi(rest[2])
	if err != nil {
		return Block{}, fmt.Errorf("bad count in %q: %w", line, err)
	}
	return Block{File: file, StartLine: startLine, EndLine: endLine, NumStmt: numStmt, Count: count}, nil
}

var hunkHeaderPattern = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ChangedLines parses a `git diff --unified=0` (unified diff with zero
// context lines) and returns, per new-file path, the set of new-file line
// numbers the diff added or modified. Zero context is required: with it, a
// hunk's "+" range names exactly the touched lines, so no per-line "+"
// scanning is needed and pure deletions (which have no "+" range) are
// naturally excluded.
func ChangedLines(diff string) map[string]map[int]bool {
	result := make(map[string]map[int]bool)
	var currentFile string
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			path := strings.TrimPrefix(line, "+++ ")
			path = strings.TrimPrefix(path, "b/")
			if path == "/dev/null" {
				currentFile = ""
				continue
			}
			currentFile = path
		case strings.HasPrefix(line, "@@ "):
			if currentFile == "" {
				continue
			}
			m := hunkHeaderPattern.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			if count == 0 {
				continue
			}
			set := result[currentFile]
			if set == nil {
				set = make(map[int]bool, count)
				result[currentFile] = set
			}
			for l := start; l < start+count; l++ {
				set[l] = true
			}
		}
	}
	return result
}

// Result holds the outcome of measuring diff coverage.
type Result struct {
	CoveredStmts int
	TotalStmts   int
	Uncovered    []Block // in-scope blocks with zero hit count, for reporting
}

// Percent returns the diff-coverage percentage, or 100 when there are no
// in-scope statements (nothing coverable was changed).
func (r Result) Percent() float64 {
	if r.TotalStmts == 0 {
		return 100
	}
	return 100 * float64(r.CoveredStmts) / float64(r.TotalStmts)
}

// Compute intersects coverage blocks with the changed-line set: a block is
// "in scope" if any line in its [StartLine, EndLine] range was added or
// modified. blockFileToDiffFile normalizes a profile's file field (typically
// "module/path/pkg/file.go") into the diff's repo-relative path form.
func Compute(blocks []Block, changed map[string]map[int]bool, blockFileToDiffFile func(string) string) Result {
	var res Result
	for _, b := range blocks {
		diffFile := blockFileToDiffFile(b.File)
		lines, ok := changed[diffFile]
		if !ok {
			continue
		}
		inScope := false
		for l := b.StartLine; l <= b.EndLine; l++ {
			if lines[l] {
				inScope = true
				break
			}
		}
		if !inScope {
			continue
		}
		res.TotalStmts += b.NumStmt
		if b.Count > 0 {
			res.CoveredStmts += b.NumStmt
		} else {
			res.Uncovered = append(res.Uncovered, b)
		}
	}
	return res
}
