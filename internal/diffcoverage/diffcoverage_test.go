package diffcoverage

import (
	"strings"
	"testing"
)

const sampleProfile = `mode: set
github.com/jmrGrav/mcp-hugo-server-go/internal/foo/foo.go:10.2,12.3 2 1
github.com/jmrGrav/mcp-hugo-server-go/internal/foo/foo.go:15.2,15.10 1 0
github.com/jmrGrav/mcp-hugo-server-go/internal/bar/bar.go:5.2,7.3 3 0
`

func TestParseProfile(t *testing.T) {
	blocks, err := ParseProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("ParseProfile() len = %d, want 3", len(blocks))
	}
	want := Block{File: "github.com/jmrGrav/mcp-hugo-server-go/internal/foo/foo.go", StartLine: 10, EndLine: 12, NumStmt: 2, Count: 1}
	if blocks[0] != want {
		t.Fatalf("ParseProfile()[0] = %+v, want %+v", blocks[0], want)
	}
}

func TestParseProfileRejectsMalformedLine(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nnot a profile line\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on malformed line")
	}
}

func TestParseProfileRejectsBadFieldCount(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nfile.go:1.1,2.1 1\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on missing count field")
	}
}

func TestParseProfileRejectsMissingColon(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nfile.go 1.1,2.1 1 1\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on missing ':'")
	}
}

func TestParseProfileRejectsBadPositionField(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nfile.go:1.1-2.1 1 1\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on missing ',' in position field")
	}
}

func TestParseProfileRejectsBadStartLine(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nfile.go:x.1,2.1 1 1\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on non-numeric start line")
	}
}

func TestParseProfileRejectsBadEndLine(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nfile.go:1.1,x.1 1 1\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on non-numeric end line")
	}
}

func TestParseProfileRejectsBadNumStmt(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nfile.go:1.1,2.1 x 1\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on non-numeric numStmt")
	}
}

func TestParseProfileRejectsBadCount(t *testing.T) {
	if _, err := ParseProfile(strings.NewReader("mode: set\nfile.go:1.1,2.1 1 x\n")); err == nil {
		t.Fatal("ParseProfile() error = nil, want error on non-numeric count")
	}
}

func TestParseProfileSkipsBlankLines(t *testing.T) {
	blocks, err := ParseProfile(strings.NewReader("mode: set\n\nfile.go:1.1,2.1 1 1\n\n"))
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("ParseProfile() len = %d, want 1 (blank lines skipped)", len(blocks))
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errBoom }

var errBoom = errBoomType{}

type errBoomType struct{}

func (errBoomType) Error() string { return "boom" }

func TestParseProfilePropagatesScannerError(t *testing.T) {
	if _, err := ParseProfile(failingReader{}); err == nil {
		t.Fatal("ParseProfile() error = nil, want the underlying reader error")
	}
}

func TestChangedLinesHunkWithZeroAddedLines(t *testing.T) {
	// A hunk that only deletes lines still carries a "+" range in unified
	// diff form (e.g. "+5,0"), naming the insertion point with zero length —
	// must not record a changed line for it.
	diff := "+++ b/foo.go\n@@ -3,2 +5,0 @@\n-old1\n-old2\n"
	changed := ChangedLines(diff)
	if lines, ok := changed["foo.go"]; ok && len(lines) != 0 {
		t.Fatalf("ChangedLines() = %v, want no changed lines for a pure-deletion hunk", lines)
	}
}

func TestParseProfileNoModeHeader(t *testing.T) {
	// A profile that (unusually) starts directly with a data line, no
	// "mode:" header, must still parse — only line 1 is special-cased.
	blocks, err := ParseProfile(strings.NewReader("github.com/x/y/z.go:1.1,2.1 1 1\n"))
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("ParseProfile() len = %d, want 1", len(blocks))
	}
}

const sampleDiff = `diff --git a/internal/foo/foo.go b/internal/foo/foo.go
index abc..def 100644
--- a/internal/foo/foo.go
+++ b/internal/foo/foo.go
@@ -9,0 +10,3 @@ func Foo() {
+line10
+line11
+line12
@@ -20 +23 @@ func Bar() {
-old
+new
diff --git a/internal/removed/removed.go b/internal/removed/removed.go
deleted file mode 100644
index abc..000
--- a/internal/removed/removed.go
+++ /dev/null
@@ -1,5 +0,0 @@
-gone
`

func TestChangedLines(t *testing.T) {
	changed := ChangedLines(sampleDiff)
	foo := changed["internal/foo/foo.go"]
	if foo == nil {
		t.Fatal("ChangedLines() missing internal/foo/foo.go")
	}
	for _, l := range []int{10, 11, 12, 23} {
		if !foo[l] {
			t.Errorf("ChangedLines()[foo.go][%d] = false, want true", l)
		}
	}
	if len(foo) != 4 {
		t.Fatalf("ChangedLines()[foo.go] = %v, want exactly 4 lines", foo)
	}
	if _, ok := changed["internal/removed/removed.go"]; ok {
		t.Fatal("ChangedLines() should not report lines for a deleted file (+++ /dev/null)")
	}
}

func TestChangedLinesIgnoresHunkBeforeAnyFileHeader(t *testing.T) {
	// A malformed/truncated diff whose first hunk appears before any "+++"
	// line must not panic on an empty currentFile.
	changed := ChangedLines("@@ -1,0 +1,2 @@\n+a\n+b\n")
	if len(changed) != 0 {
		t.Fatalf("ChangedLines() = %v, want empty (no file header seen)", changed)
	}
}

func TestChangedLinesSkipsUnparsableHunkHeader(t *testing.T) {
	diff := "+++ b/foo.go\n@@ garbage @@\n+x\n"
	changed := ChangedLines(diff)
	if lines, ok := changed["foo.go"]; ok && len(lines) != 0 {
		t.Fatalf("ChangedLines() = %v, want no lines recorded for an unparsable hunk header", lines)
	}
}

func TestChangedLinesEmptyDiff(t *testing.T) {
	if changed := ChangedLines(""); len(changed) != 0 {
		t.Fatalf("ChangedLines(\"\") = %v, want empty", changed)
	}
}

func identity(s string) string {
	return strings.TrimPrefix(s, "github.com/jmrGrav/mcp-hugo-server-go/")
}

func TestComputeCoversOnlyInScopeBlocks(t *testing.T) {
	blocks, err := ParseProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	// Only lines 10-12 of foo.go are "changed"; bar.go and foo.go's line 15
	// block are untouched by this diff and must not count.
	changed := map[string]map[int]bool{
		"internal/foo/foo.go": {10: true, 11: true, 12: true},
	}
	res := Compute(blocks, changed, identity)
	if res.TotalStmts != 2 {
		t.Fatalf("Compute() TotalStmts = %d, want 2 (only the covered 10-12 block in scope)", res.TotalStmts)
	}
	if res.CoveredStmts != 2 {
		t.Fatalf("Compute() CoveredStmts = %d, want 2", res.CoveredStmts)
	}
	if got := res.Percent(); got != 100 {
		t.Fatalf("Compute().Percent() = %v, want 100", got)
	}
}

func TestComputeReportsUncoveredInScopeBlock(t *testing.T) {
	blocks, err := ParseProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	changed := map[string]map[int]bool{
		"internal/foo/foo.go": {15: true},
	}
	res := Compute(blocks, changed, identity)
	if res.TotalStmts != 1 || res.CoveredStmts != 0 {
		t.Fatalf("Compute() = %+v, want TotalStmts=1 CoveredStmts=0", res)
	}
	if len(res.Uncovered) != 1 || res.Uncovered[0].StartLine != 15 {
		t.Fatalf("Compute().Uncovered = %+v, want the line-15 block", res.Uncovered)
	}
	if got := res.Percent(); got != 0 {
		t.Fatalf("Compute().Percent() = %v, want 0", got)
	}
}

func TestResultPercentNoStatementsIsFullMarks(t *testing.T) {
	var res Result
	if got := res.Percent(); got != 100 {
		t.Fatalf("Result{}.Percent() = %v, want 100 (nothing coverable changed)", got)
	}
}

func TestComputeIgnoresFilesNotInDiff(t *testing.T) {
	blocks, err := ParseProfile(strings.NewReader(sampleProfile))
	if err != nil {
		t.Fatal(err)
	}
	res := Compute(blocks, map[string]map[int]bool{}, identity)
	if res.TotalStmts != 0 {
		t.Fatalf("Compute() TotalStmts = %d, want 0 when nothing changed", res.TotalStmts)
	}
}
