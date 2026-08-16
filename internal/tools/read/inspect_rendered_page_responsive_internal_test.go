package read

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func mustParseFragment(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader("<!DOCTYPE html><html><body>" + body + "</body></html>"))
	if err != nil {
		t.Fatalf("html.Parse: %v", err)
	}
	return doc
}

func TestComputeResponsiveChecksFlagsFixedWidthUnwrappedTable(t *testing.T) {
	doc := mustParseFragment(t, `<table style="width:900px"><tr><td>a</td></tr></table>`)
	got := computeResponsiveChecks(doc)
	if got.Tables.Count != 1 {
		t.Fatalf("Tables.Count = %d, want 1", got.Tables.Count)
	}
	if got.Tables.FixedWidth != 1 {
		t.Fatalf("Tables.FixedWidth = %d, want 1", got.Tables.FixedWidth)
	}
	if got.Tables.ResponsiveWrapper != 0 {
		t.Fatalf("Tables.ResponsiveWrapper = %d, want 0 (no wrapper present)", got.Tables.ResponsiveWrapper)
	}
}

func TestComputeResponsiveChecksRecognisesResponsiveWrapper(t *testing.T) {
	doc := mustParseFragment(t, `<div class="table-responsive"><table style="width:900px"><tr><td>a</td></tr></table></div>`)
	got := computeResponsiveChecks(doc)
	if got.Tables.ResponsiveWrapper != 1 {
		t.Fatalf("Tables.ResponsiveWrapper = %d, want 1 when ancestor has table-responsive class", got.Tables.ResponsiveWrapper)
	}
}

func TestComputeResponsiveChecksFlagsLongUnbreakableCell(t *testing.T) {
	longToken := strings.Repeat("a", 40)
	doc := mustParseFragment(t, `<table><tr><td>`+longToken+`</td></tr></table>`)
	got := computeResponsiveChecks(doc)
	if got.Tables.LongCellRisk != 1 {
		t.Fatalf("Tables.LongCellRisk = %d, want 1 for a 40-char unbreakable token", got.Tables.LongCellRisk)
	}
}

func TestComputeResponsiveChecksIgnoresNormalTable(t *testing.T) {
	doc := mustParseFragment(t, `<table><tr><td>ok</td><td>fine</td></tr></table>`)
	got := computeResponsiveChecks(doc)
	if got.Tables.Count != 1 {
		t.Fatalf("Tables.Count = %d, want 1", got.Tables.Count)
	}
	if got.Tables.FixedWidth != 0 || got.Tables.LongCellRisk != 0 {
		t.Fatalf("expected no risk flags on a normal table, got %+v", got.Tables)
	}
}

func TestComputeResponsiveChecksCodeBlockOverflowUnsafeOnFixedWidth(t *testing.T) {
	doc := mustParseFragment(t, `<pre style="width:800px">some code</pre>`)
	got := computeResponsiveChecks(doc)
	if got.CodeBlocks.Count != 1 {
		t.Fatalf("CodeBlocks.Count = %d, want 1", got.CodeBlocks.Count)
	}
	if got.CodeBlocks.OverflowSafe {
		t.Fatalf("OverflowSafe = true, want false for a fixed-width <pre>")
	}
}

func TestComputeResponsiveChecksCodeBlockSafeByDefault(t *testing.T) {
	doc := mustParseFragment(t, `<pre>some code</pre>`)
	got := computeResponsiveChecks(doc)
	if !got.CodeBlocks.OverflowSafe {
		t.Fatalf("OverflowSafe = false, want true for a plain <pre> with no fixed width/nowrap")
	}
}

func TestComputeResponsiveChecksImageFixedOverlargeWidthUnsafe(t *testing.T) {
	doc := mustParseFragment(t, `<img src="/x.png" width="800">`)
	got := computeResponsiveChecks(doc)
	if got.Images.Count != 1 {
		t.Fatalf("Images.Count = %d, want 1", got.Images.Count)
	}
	if got.Images.Responsive {
		t.Fatalf("Responsive = true, want false for a hardcoded width=800 image")
	}
}

func TestComputeResponsiveChecksOverflowHiddenIsNotAWrapper(t *testing.T) {
	doc := mustParseFragment(t, `<div class="overflow-hidden"><table style="width:900px"><tr><td>a</td></tr></table></div>`)
	got := computeResponsiveChecks(doc)
	if got.Tables.ResponsiveWrapper != 0 {
		t.Fatalf("Tables.ResponsiveWrapper = %d, want 0 (overflow-hidden clips, it does not scroll)", got.Tables.ResponsiveWrapper)
	}
}

func TestComputeResponsiveChecksMaxWidthIsNotFixedWidth(t *testing.T) {
	doc := mustParseFragment(t, `<table style="max-width: 900px"><tr><td>a</td></tr></table>`)
	got := computeResponsiveChecks(doc)
	if got.Tables.FixedWidth != 0 {
		t.Fatalf("Tables.FixedWidth = %d, want 0 (max-width is the responsive-friendly declaration)", got.Tables.FixedWidth)
	}
}

func TestComputeResponsiveChecksSkipsChromaLineNumberTable(t *testing.T) {
	longToken := strings.Repeat("a", 40)
	doc := mustParseFragment(t, `<div class="highlight"><table class="lntable"><tr><td class="lntd"><pre>1</pre></td><td class="lntd"><pre>`+longToken+`</pre></td></tr></table></div>`)
	got := computeResponsiveChecks(doc)
	if got.Tables.Count != 0 {
		t.Fatalf("Tables.Count = %d, want 0 (chroma line-number tables must be excluded)", got.Tables.Count)
	}
}

func TestComputeResponsiveChecksImageWithSrcsetIsResponsive(t *testing.T) {
	doc := mustParseFragment(t, `<img src="/x.png" width="800" srcset="/x-400.png 400w, /x-800.png 800w">`)
	got := computeResponsiveChecks(doc)
	if !got.Images.Responsive {
		t.Fatalf("Responsive = false, want true when the image has a srcset escape hatch")
	}
}
