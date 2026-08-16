package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"golang.org/x/image/font/sfnt"
	"image"
	"image/color"
	"image/jpeg"
)

func TestDefs(t *testing.T) {
	defs := Defs()
	if len(defs) != 20 {
		t.Fatalf("Defs() = %d, want 20", len(defs))
	}
	if defs[0].RequiredScope != "write" {
		t.Fatalf("Defs() first scope = %q", defs[0].RequiredScope)
	}
}

func TestFetchImageErrorBranches(t *testing.T) {
	if _, err := fetchImage(context.Background(), "://bad-url", "", "prompt"); err == nil {
		t.Fatal("fetchImage() should fail on malformed URL")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	if _, err := fetchImage(context.Background(), srv.URL, "", "prompt"); err == nil {
		t.Fatal("fetchImage() should fail on non-2xx response")
	}
}

func TestLogicalHugoRootPath(t *testing.T) {
	tests := []struct {
		name     string
		hugoRoot string
		absPath  string
		want     string
	}{
		{"under hugo root", "/srv/hugo-site", "/srv/hugo-site/static/images/hello-featured.jpg", "static/images/hello-featured.jpg"},
		{"empty hugo root falls back to input", "", "/srv/hugo-site/static/images/hello-featured.jpg", ""},
		{"empty path", "/srv/hugo-site", "", ""},
		{"outside hugo root", "/srv/hugo-site", "/etc/passwd", ""},
		{"already relative", "/srv/hugo-site", "static/images/hello-featured.jpg", "static/images/hello-featured.jpg"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := logicalHugoRootPath(tc.hugoRoot, tc.absPath); got != tc.want {
				t.Fatalf("logicalHugoRootPath(%q, %q) = %q, want %q", tc.hugoRoot, tc.absPath, got, tc.want)
			}
		})
	}
}

func TestResolveHeroImageLocation(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveHeroImageLocation(root, "posts/demo")
	if err != nil {
		t.Fatalf("ResolveHeroImageLocation() error = %v", err)
	}
	wantAbs := filepath.Join(root, "static", "images", "posts", "demo"+HeroImageSuffix)
	if got.AbsPath != wantAbs {
		t.Fatalf("AbsPath = %q, want %q", got.AbsPath, wantAbs)
	}
	if got.LogicalPath != "static/images/posts/demo"+HeroImageSuffix {
		t.Fatalf("LogicalPath = %q", got.LogicalPath)
	}
	if got.Name != "demo"+HeroImageSuffix {
		t.Fatalf("Name = %q, want %q", got.Name, "demo"+HeroImageSuffix)
	}
}

func TestResolveHeroImageLocationRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolveHeroImageLocation(root, "../escape"); err == nil {
		t.Fatal("ResolveHeroImageLocation() error = nil, want traversal rejection")
	}
}

func TestPublicImagePathFromLogical(t *testing.T) {
	if got := publicImagePathFromLogical("static/images/posts/demo-featured.jpg"); got != "/images/posts/demo-featured.jpg" {
		t.Fatalf("publicImagePathFromLogical(static path) = %q", got)
	}
	if got := publicImagePathFromLogical("images/posts/demo-featured.jpg"); got != "" {
		t.Fatalf("publicImagePathFromLogical(non-static path) = %q, want empty", got)
	}
}

func TestNormalizeHeroImageSlug(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "source key", in: "posts/demo", want: "posts/demo"},
		{name: "public slug", in: "/posts/demo/", want: "posts/demo"},
		{name: "language-prefixed public slug", in: "/fr/posts/demo/", want: "posts/demo"},
		{name: "empty", in: "   ", want: ""},
		{name: "bad public slug missing trailing slash", in: "/posts/demo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeHeroImageSlug(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatal("normalizeHeroImageSlug() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeHeroImageSlug() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeHeroImageSlug() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderFeaturedImageSiteNameNotHardcoded is a regression test for a
// deployment-portability bug: renderFeaturedImage used to draw the literal
// string "arleo.eu" (the original production deployment's own domain) as a
// brand mark on every generated hero image, regardless of who was running
// this server. It's now the caller-supplied siteName (config.Config.SiteName),
// and is skipped entirely when unset rather than falling back to any
// hardcoded brand string.
func TestRenderFeaturedImageSiteNameNotHardcoded(t *testing.T) {
	for _, siteName := range []string{"My Blog", ""} {
		t.Run("siteName="+siteName, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "out.jpg")
			if err := renderFeaturedImage(dir, path, "", "A Title", "", nil, "#4c8bf5", siteName); err != nil {
				t.Fatalf("renderFeaturedImage() error = %v", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("output file not found: %v", err)
			}
			if info.Size() < 1000 {
				t.Fatalf("rendered JPEG suspiciously small: %d bytes", info.Size())
			}
		})
	}
}

func writeJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("os.Create(%s): %v", path, err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("jpeg.Encode(%s): %v", path, err)
	}
}

func TestLoadPhotoBackground(t *testing.T) {
	dir := t.TempDir()
	title := "hello world"
	name := selectBackground(title)
	writeJPEG(t, filepath.Join(dir, name), 1200, 675)

	got, err := loadPhotoBackground(dir, title, 1200, 675)
	if err != nil {
		t.Fatalf("loadPhotoBackground() error = %v", err)
	}
	if got.Bounds().Dx() != 1200 || got.Bounds().Dy() != 675 {
		t.Fatalf("bounds = %v, want 1200x675", got.Bounds())
	}
}

func TestLoadPhotoBackgroundRejectsWrongSize(t *testing.T) {
	dir := t.TempDir()
	title := "hello world"
	name := selectBackground(title)
	writeJPEG(t, filepath.Join(dir, name), 100, 100)

	if _, err := loadPhotoBackground(dir, title, 1200, 675); err == nil {
		t.Fatal("loadPhotoBackground() error = nil, want size mismatch")
	}
}

func TestLoadPhotoBackgroundRejectsInvalidJPEG(t *testing.T) {
	dir := t.TempDir()
	title := "hello world"
	name := selectBackground(title)
	if err := os.WriteFile(filepath.Join(dir, name), []byte("not-a-jpeg"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := loadPhotoBackground(dir, title, 1200, 675); err == nil {
		t.Fatal("loadPhotoBackground() error = nil, want decode failure")
	}
}

func TestColorHelpers(t *testing.T) {
	if got := colorFromHex("#7aa2f7"); got != (color.RGBA{R: 122, G: 162, B: 247, A: 255}) {
		t.Fatalf("colorFromHex() = %#v", got)
	}
	if got := colorFromHex("bad"); got != (color.RGBA{}) {
		t.Fatalf("colorFromHex(bad) = %#v, want zero", got)
	}
	if got := mustHexColor("bad"); got != (color.RGBA{R: 122, G: 162, B: 247, A: 255}) {
		t.Fatalf("mustHexColor(bad) = %#v", got)
	}
	if got := withAlpha(color.RGBA{R: 1, G: 2, B: 3, A: 4}, 200); got != (color.RGBA{R: 1, G: 2, B: 3, A: 200}) {
		t.Fatalf("withAlpha() = %#v", got)
	}
	if got, err := hexToRGB("7aa2f7"); err != nil || got != [3]byte{122, 162, 247} {
		t.Fatalf("hexToRGB() = %#v, %v", got, err)
	}
	if _, err := hexToRGB("xyz"); err == nil {
		t.Fatal("hexToRGB(invalid) error = nil, want error")
	}
	if got, err := parseHexByte("7a"); err != nil || got != 122 {
		t.Fatalf("parseHexByte() = %v, %v", got, err)
	}
	if _, err := parseHexByte("x"); err == nil {
		t.Fatal("parseHexByte(short) error = nil, want error")
	}
	if got, ok := hexVal('F'); !ok || got != 15 {
		t.Fatalf("hexVal('F') = (%v, %v), want (15, true)", got, ok)
	}
	if _, ok := hexVal('x'); ok {
		t.Fatal("hexVal('x') ok = true, want false")
	}
}

// TestHeroFontCoversArrowAndAccents is a regression test for #812: the old
// basicfont.Face7x13 debug font silently dropped glyphs like "→" (U+2192),
// baking a missing-glyph tofu box into any hero image whose title used a
// character outside its tiny coverage. gobold must render both the arrow
// and the accented French characters used throughout this site's content.
func TestHeroFontCoversArrowAndAccents(t *testing.T) {
	f, err := loadHeroFont()
	if err != nil {
		t.Fatalf("loadHeroFont() error = %v", err)
	}
	var buf sfnt.Buffer
	for _, r := range []rune{'→', 'é', 'è', 'ê', 'ç', 'à', 'œ'} {
		idx, err := f.GlyphIndex(&buf, r)
		if err != nil {
			t.Fatalf("GlyphIndex(%q) error = %v", r, err)
		}
		if idx == 0 {
			t.Errorf("GlyphIndex(%q) = 0 (glyph .notdef), font does not cover this rune", r)
		}
	}
}

// TestRenderFeaturedImageLongTitleWraps is a regression test for #812: with
// the old fixed 18px line height sized for a 13px bitmap font, a title long
// enough to wrap onto a second line at the new, much larger display font
// size would overlap the subtitle/tags fixed below it. renderFeaturedImage
// must still succeed (not panic/error) and produce a normally-sized JPEG
// for a title long enough to wrap.
func TestRenderFeaturedImageLongTitleWraps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.jpg")
	longTitle := "MCP Hugo Server passe public : npm, npx et Claude Desktop (MCPB) avec support complet"
	if err := renderFeaturedImage(dir, path, "tech", longTitle, "A subtitle", []string{"mcp", "claude"}, "#7aa2f7", "arleo.eu"); err != nil {
		t.Fatalf("renderFeaturedImage() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("output file not found: %v", err)
	}
	if info.Size() < 1000 {
		t.Fatalf("rendered JPEG suspiciously small: %d bytes", info.Size())
	}
}

func TestStableUint64HashDeterministic(t *testing.T) {
	if got1, got2 := stableUint64Hash("hello world"), stableUint64Hash("hello world"); got1 != got2 {
		t.Fatalf("stableUint64Hash() not deterministic: %d vs %d", got1, got2)
	}
	if got1, got2 := stableUint64Hash("hello world"), stableUint64Hash("different"); got1 == got2 {
		t.Fatalf("stableUint64Hash() collision on trivial inputs: %d", got1)
	}
}

func TestRegisterNilServer(t *testing.T) {
	Register(nil, config.Default(), nil, nil)
	RegisterSiteAdmin(nil, config.Default(), nil, nil)
	RegisterBuild(nil, config.Default(), nil, nil)
	RegisterPreviewBuild(nil, config.Default())
	RegisterHooks(nil, config.Default())
	RegisterSRI(nil, config.Default())
}
