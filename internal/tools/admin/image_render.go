package admin

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// backgroundFiles lists the Unsplash photo filenames stored in featured-backgrounds/.
// Order is fixed so md5(title)%6 selects consistently across runs.
var backgroundFiles = []string{
	"0c180e9a.jpg",
	"6727bbee.jpg",
	"89a0578e.jpg",
	"a42e9bc5.jpg",
	"cc51402b.jpg",
	"dbce31ce.jpg",
}

// renderFeaturedImage generates a 1200×675 JPEG to path using a background photo
// from bgDir composited with a dark gradient overlay, title, and tags.
// Falls back to a solid gradient if no background photos are found.
func renderFeaturedImage(bgDir, path, style, title, subtitle string, tags []string, accent string) error {
	const (
		width  = 1200
		height = 675
	)

	canvas, err := loadPhotoBackground(bgDir, title, width, height)
	if err != nil {
		// Fallback: solid gradient background
		canvas = gradientBackground(style, title, width, height)
	}

	drawDarkOverlay(canvas, width, height)

	accentRGBA := mustHexColor(accent)
	drawFillRect(canvas, 0, 0, 8, height, accentRGBA)
	drawFillRect(canvas, 8, height-6, width-8, 6, withAlpha(accentRGBA, 110))
	drawCircle(canvas, 72, 54, 16, withAlpha(accentRGBA, 45))
	drawCircle(canvas, 72, 54, 5, accentRGBA)

	drawImgText(canvas, 96, 60, "arleo.eu", accentRGBA)
	drawTitle(canvas, 60, 438, title, accentRGBA)
	if subtitle != "" {
		drawWrappedText(canvas, 60, 500, subtitle, color.RGBA{235, 235, 235, 255}, 980)
	}
	for i, tag := range tags {
		if i >= 6 {
			break
		}
		x := 60 + i*178
		drawRoundedRect(canvas, x, 610, 160, 28, color.RGBA{0, 0, 0, 140}, withAlpha(accentRGBA, 200))
		drawCenteredText(canvas, x, 617, 160, "#"+tag, accentRGBA)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: 88}); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// selectBackground picks one of the 6 background filenames deterministically from title.
func selectBackground(title string) string {
	h := md5.Sum([]byte(title))
	n := binary.BigEndian.Uint64(h[:8])
	return backgroundFiles[n%uint64(len(backgroundFiles))]
}

// loadPhotoBackground opens the selected background JPEG and copies it onto a 1200×675 canvas.
// Backgrounds must be exactly width×height pixels; any other size falls back to gradient.
func loadPhotoBackground(bgDir, title string, width, height int) (*image.RGBA, error) {
	name := selectBackground(title)
	bgPath := filepath.Join(bgDir, name)
	f, err := os.Open(bgPath)
	if err != nil {
		return nil, fmt.Errorf("open background %s: %w", bgPath, err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode background %s: %w", bgPath, err)
	}
	b := img.Bounds()
	if b.Dx() != width || b.Dy() != height {
		return nil, fmt.Errorf("background %s is %dx%d, want %dx%d", name, b.Dx(), b.Dy(), width, height)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(canvas, canvas.Bounds(), img, image.Point{}, draw.Src)
	return canvas, nil
}

// drawDarkOverlay composites a semi-transparent dark gradient over the photo
// to ensure title and tag text remain legible.
func drawDarkOverlay(img *image.RGBA, width, height int) {
	for y := 0; y < height; y++ {
		ratio := float64(y) / float64(height-1)
		// alpha goes from ~140 at top to ~195 at bottom
		alpha := uint8(math.Round(140 + ratio*55))
		stripe := image.Rect(0, y, width, y+1)
		draw.Draw(img, stripe, image.NewUniform(color.RGBA{0, 0, 0, alpha}), image.Point{}, draw.Over)
	}
}

// gradientBackground returns a solid gradient canvas as a fallback when no photo backgrounds are available.
func gradientBackground(style, title string, width, height int) *image.RGBA {
	bg1, bg2 := featuredImagePalette(style, title)
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		ratio := float64(y) / float64(height-1)
		row := blendColor(bg1, bg2, ratio)
		for x := 0; x < width; x++ {
			canvas.SetRGBA(x, y, row)
		}
	}
	return canvas
}

func featuredImagePalette(style, title string) (color.RGBA, color.RGBA) {
	h := md5.Sum([]byte(style + "::" + title))
	base := colorFromHex(map[string]string{
		"geo":  "#2a2254",
		"tech": "#14243f",
	}[style])
	if base == (color.RGBA{}) {
		base = colorFromHex("#1a1b26")
	}
	shift := func(v byte, delta int) byte {
		n := int(v) + delta
		switch {
		case n < 0:
			return 0
		case n > 255:
			return 255
		default:
			return byte(n)
		}
	}
	variant := color.RGBA{
		R: shift(base.R, int(h[0]%24)-12),
		G: shift(base.G, int(h[1]%18)-9),
		B: shift(base.B, int(h[2]%20)-10),
		A: 255,
	}
	return base, variant
}

func drawTitle(img *image.RGBA, x, y int, title string, accent color.RGBA) {
	drawWrappedText(img, x, y, title, color.RGBA{255, 255, 255, 255}, 1040)
	drawFillRect(img, x, y+20, 64, 4, accent)
}

func drawImgText(img *image.RGBA, x, y int, text string, clr color.RGBA) {
	drawImgString(img, x, y, text, clr, basicfont.Face7x13)
}

func drawCenteredText(img *image.RGBA, x, y, w int, text string, clr color.RGBA) {
	face := basicfont.Face7x13
	d := &font.Drawer{Dst: img, Src: image.NewUniform(clr), Face: face}
	textWidth := d.MeasureString(text).Round()
	startX := x + (w-textWidth)/2
	if startX < x+4 {
		startX = x + 4
	}
	drawImgString(img, startX, y, text, clr, face)
}

func drawWrappedText(img *image.RGBA, x, y int, text string, clr color.RGBA, maxWidth int) {
	lines := wrapText(text, basicfont.Face7x13, maxWidth)
	for i, line := range lines {
		drawImgString(img, x, y+i*18, line, clr, basicfont.Face7x13)
	}
}

func drawImgString(img *image.RGBA, x, y int, text string, clr color.RGBA, face font.Face) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(clr),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(text)
}

func wrapText(text string, face font.Face, maxWidth int) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	lines := make([]string, 0, 4)
	current := words[0]
	measure := func(s string) int {
		d := &font.Drawer{Face: face}
		return d.MeasureString(s).Round()
	}
	for _, word := range words[1:] {
		candidate := current + " " + word
		if measure(candidate) > maxWidth && current != "" {
			lines = append(lines, current)
			current = word
			continue
		}
		current = candidate
	}
	lines = append(lines, current)
	if len(lines) == 1 && measure(lines[0]) > maxWidth {
		runes := []rune(lines[0])
		mid := len(runes) / 2
		lines = []string{strings.TrimSpace(string(runes[:mid])), strings.TrimSpace(string(runes[mid:]))}
	}
	return lines
}

func drawFillRect(img *image.RGBA, x, y, w, h int, clr color.RGBA) {
	r := image.Rect(x, y, x+w, y+h)
	draw.Draw(img, r, image.NewUniform(clr), image.Point{}, draw.Src)
}

func drawRoundedRect(img *image.RGBA, x, y, w, h int, fill color.RGBA, stroke color.RGBA) {
	drawFillRect(img, x, y, w, h, fill)
	drawFillRect(img, x, y, w, 1, stroke)
	drawFillRect(img, x, y+h-1, w, 1, stroke)
	drawFillRect(img, x, y, 1, h, stroke)
	drawFillRect(img, x+w-1, y, 1, h, stroke)
}

func drawCircle(img *image.RGBA, cx, cy, radius int, clr color.RGBA) {
	r2 := radius * radius
	for y := cy - radius; y <= cy+radius; y++ {
		for x := cx - radius; x <= cx+radius; x++ {
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= r2 && image.Pt(x, y).In(img.Bounds()) {
				img.SetRGBA(x, y, clr)
			}
		}
	}
}

func blendColor(a, b color.RGBA, ratio float64) color.RGBA {
	clamp := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(math.Round(v))
	}
	return color.RGBA{
		R: clamp(float64(a.R) + (float64(b.R)-float64(a.R))*ratio),
		G: clamp(float64(a.G) + (float64(b.G)-float64(a.G))*ratio),
		B: clamp(float64(a.B) + (float64(b.B)-float64(a.B))*ratio),
		A: 255,
	}
}

func colorFromHex(spec string) color.RGBA {
	var c color.RGBA
	if len(spec) != 7 || spec[0] != '#' {
		return c
	}
	raw, err := hexToRGB(spec[1:])
	if err != nil {
		return c
	}
	return color.RGBA{R: raw[0], G: raw[1], B: raw[2], A: 255}
}

func mustHexColor(hexStr string) color.RGBA {
	c := colorFromHex(hexStr)
	if c == (color.RGBA{}) {
		return color.RGBA{R: 122, G: 162, B: 247, A: 255}
	}
	return c
}

func withAlpha(c color.RGBA, alpha uint8) color.RGBA {
	c.A = alpha
	return c
}

func hexToRGB(s string) ([3]byte, error) {
	var out [3]byte
	if len(s) != 6 {
		return out, fmt.Errorf("invalid hex color")
	}
	b := make([]byte, 3)
	for i := 0; i < 3; i++ {
		n, err := parseHexByte(s[i*2 : i*2+2])
		if err != nil {
			return out, fmt.Errorf("invalid hex color")
		}
		b[i] = n
	}
	copy(out[:], b)
	return out, nil
}

func parseHexByte(s string) (byte, error) {
	if len(s) != 2 {
		return 0, fmt.Errorf("bad byte")
	}
	hi, ok1 := hexVal(s[0])
	lo, ok2 := hexVal(s[1])
	if !ok1 || !ok2 {
		return 0, fmt.Errorf("bad byte")
	}
	return hi<<4 | lo, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
