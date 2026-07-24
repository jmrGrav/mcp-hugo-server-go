package write

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// allowedSVGElements is a strict allowlist (not a blocklist) of SVG element
// names accepted in an uploaded asset (#571) — shape drawing, text, and
// basic reuse/gradient/pattern constructs. Deliberately excludes anything
// capable of pulling in or executing external/dynamic content: "script",
// "style" (CSS can carry url() references and, on old renderers,
// expression()), "foreignObject" (embeds arbitrary non-SVG markup,
// including HTML), "image" (raster embedding via a URL/data URI), and
// "animate"/"animateTransform"/"set" (SMIL animation, a known XSS vector in
// some renderers via animated attribute injection).
var allowedSVGElements = map[string]bool{
	"svg": true, "g": true, "path": true, "rect": true, "circle": true,
	"ellipse": true, "line": true, "polyline": true, "polygon": true,
	"text": true, "tspan": true, "defs": true, "use": true, "symbol": true,
	"clippath": true, "mask": true, "lineargradient": true, "radialgradient": true,
	"stop": true, "title": true, "desc": true, "marker": true, "pattern": true,
}

// allowedSVGAttributes is a strict allowlist of attribute names accepted on
// any allowed element. "href"/"xlink:href" (matched by local name, so both
// forms hit the same case) are handled separately below — allowed only as a
// local fragment reference ("#id"), never a URL. Deliberately excludes
// "style" (see allowedSVGElements) and every "on*" event-handler attribute,
// the latter also checked explicitly regardless of this list so the
// rejection reason is specific.
var allowedSVGAttributes = map[string]bool{
	"id": true, "class": true, "transform": true, "viewbox": true,
	"xmlns": true, "version": true,
	"width": true, "height": true, "x": true, "y": true, "x1": true, "y1": true,
	"x2": true, "y2": true, "cx": true, "cy": true, "r": true, "rx": true, "ry": true,
	"d": true, "points": true, "fill": true, "fill-rule": true, "fill-opacity": true,
	"stroke": true, "stroke-width": true, "stroke-linecap": true, "stroke-linejoin": true,
	"stroke-dasharray": true, "stroke-opacity": true, "opacity": true,
	"offset": true, "stop-color": true, "stop-opacity": true,
	"gradientunits": true, "gradienttransform": true, "patternunits": true,
	"patterntransform": true, "markerwidth": true, "markerheight": true,
	"markerunits": true, "orient": true, "refx": true, "refy": true,
	"preserveaspectratio": true, "clip-path": true, "mask": true, "role": true,
}

// urlBearingSVGAttributes are the allowlisted attributes (besides href/
// xlink:href, handled separately) whose values can legally embed a CSS
// `url(...)` paint/reference — e.g. `fill="url(#gradient1)"` to paint with a
// locally-defined gradient/pattern, or `clip-path="url(#clip1)"` to apply a
// locally-defined clip path. Every `url(...)` occurrence inside these
// values must target a local fragment ("#id"); an external URL here is a
// stored client-side fetch to an attacker-controlled or internal host
// whenever the asset is rendered (#626), the same class of vector href's
// local-fragment restriction already exists to prevent.
var urlBearingSVGAttributes = map[string]bool{
	"fill": true, "stroke": true, "clip-path": true, "mask": true,
}

// svgURLFuncPattern matches every `url(...)` occurrence in an attribute
// value (case-insensitive, tolerant of internal whitespace), capturing the
// target so each one can be validated individually — a value can legally
// contain more than one, e.g. a (hypothetical) multi-token value, and a
// naive "trim first url( / last )" check would wrongly accept
// `url(#a) url(http://evil)` because the trimmed result still starts with
// "#".
var svgURLFuncPattern = regexp.MustCompile(`(?i)url\(\s*(.*?)\s*\)`)

// validateSVGURLBearingValue rejects an attribute value if any `url(...)`
// reference inside it does not target a local fragment ("#id").
func validateSVGURLBearingValue(local, elementName, value string) error {
	for _, match := range svgURLFuncPattern.FindAllStringSubmatch(value, -1) {
		target := strings.TrimSpace(match[1])
		target = strings.Trim(target, `"'`)
		if !strings.HasPrefix(target, "#") {
			return fmt.Errorf("invalid_svg: %q must reference only a local fragment (\"url(#id)\") on <%s>, got %q", local, elementName, value)
		}
	}
	return nil
}

// validateSVGContent rejects an uploaded SVG asset outright on any
// disallowed construct (#571) — consistent with upload_page_asset's
// existing never-trust-declared-type posture, and the safest of the two
// failure modes the issue considered: reject, don't silently strip and
// serve a modified file the caller never saw. Enforces:
//   - well-formed XML, single root <svg> element
//   - no DOCTYPE/markup declarations (the classic XXE/entity-expansion
//     smuggling vector — encoding/xml doesn't expand custom entities by
//     itself, but a declaration has no legitimate place in an uploaded
//     asset regardless)
//   - no processing instructions other than the XML declaration itself
//   - only allowlisted elements (allowedSVGElements)
//   - no "on*" event-handler attributes, checked explicitly regardless of
//     the attribute allowlist so the rejection reason names the actual risk
//   - only allowlisted attributes (allowedSVGAttributes), plus href/
//     xlink:href restricted to a local fragment reference ("#id") — never a
//     URL, data URI, or javascript: scheme
//   - every url(...) reference inside fill/stroke/clip-path/mask
//     (urlBearingSVGAttributes) restricted to a local fragment
//     ("url(#id)") — never an external host (#626)
func validateSVGContent(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("invalid_svg: XML parse error: %w", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			return fmt.Errorf("invalid_svg: DOCTYPE/markup declarations are not allowed")
		case xml.ProcInst:
			if !strings.EqualFold(t.Target, "xml") {
				return fmt.Errorf("invalid_svg: processing instructions other than the XML declaration are not allowed (found %q)", t.Target)
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if !sawRoot {
				if name != "svg" {
					return fmt.Errorf("invalid_svg: root element must be <svg>, got <%s>", name)
				}
				sawRoot = true
			}
			if !allowedSVGElements[name] {
				return fmt.Errorf("invalid_svg: disallowed element <%s>", name)
			}
			for _, attr := range t.Attr {
				// Namespace declarations (xmlns:xlink="...", etc.) are
				// parsed by Go's xml.Decoder as attributes with
				// Name.Space=="xmlns" — always allowed regardless of the
				// local name (e.g. "xlink"), since they only bind a prefix
				// and carry no executable risk themselves.
				if attr.Name.Space == "xmlns" {
					continue
				}
				local := strings.ToLower(attr.Name.Local)
				if strings.HasPrefix(local, "on") {
					return fmt.Errorf("invalid_svg: disallowed event-handler attribute %q on <%s>", local, name)
				}
				if local == "href" {
					if !strings.HasPrefix(strings.TrimSpace(attr.Value), "#") {
						return fmt.Errorf("invalid_svg: href must be a local fragment reference (\"#id\") on <%s>, got %q", name, attr.Value)
					}
					continue
				}
				if !allowedSVGAttributes[local] {
					return fmt.Errorf("invalid_svg: disallowed attribute %q on <%s>", local, name)
				}
				if urlBearingSVGAttributes[local] {
					if err := validateSVGURLBearingValue(local, name, attr.Value); err != nil {
						return err
					}
				}
			}
		}
	}
	if !sawRoot {
		return fmt.Errorf("invalid_svg: no root <svg> element found")
	}
	return nil
}
