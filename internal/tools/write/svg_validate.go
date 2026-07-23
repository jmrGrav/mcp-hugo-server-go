package write

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
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
			}
		}
	}
	if !sawRoot {
		return fmt.Errorf("invalid_svg: no root <svg> element found")
	}
	return nil
}
