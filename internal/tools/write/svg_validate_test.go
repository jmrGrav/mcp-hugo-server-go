package write

import "testing"

func TestValidateSVGContentAcceptsCleanShapeSVG(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 24 24" width="24" height="24">
		<defs>
			<linearGradient id="g1"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="#000"/></linearGradient>
		</defs>
		<path d="M0 0h24v24H0z" fill="url(#g1)"/>
		<use href="#g1"/>
		<use xlink:href="#g1"/>
	</svg>`
	if err := validateSVGContent([]byte(svg)); err != nil {
		t.Fatalf("validateSVGContent: want nil for clean SVG, got %v", err)
	}
}

func TestValidateSVGContentRejectsScript(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for <script>, got nil")
	}
}

func TestValidateSVGContentRejectsStyle(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><style>* { fill: url(javascript:alert(1)) }</style></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for <style>, got nil")
	}
}

func TestValidateSVGContentRejectsForeignObject(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><body xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></body></foreignObject></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for <foreignObject>, got nil")
	}
}

func TestValidateSVGContentRejectsImageElement(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil.example/track.png"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for <image>, got nil")
	}
}

func TestValidateSVGContentRejectsEventHandlerAttribute(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect onclick="alert(1)" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for onclick=, got nil")
	}
}

func TestValidateSVGContentRejectsExternalHref(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><use href="https://evil.example/x.svg#a"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for an external href, got nil")
	}
}

func TestValidateSVGContentRejectsJavascriptHref(t *testing.T) {
	// <use> is an allowlisted element, so this exercises the href-value
	// check itself (must be a local "#id" fragment) rather than failing
	// earlier on an unlisted element — unlike <a>, which would reject
	// before the href value is ever inspected.
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><use href="javascript:alert(1)"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for a javascript: href, got nil")
	}
}

func TestValidateSVGContentAcceptsLocalFragmentHref(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><defs><path id="p1" d="M0 0"/></defs><use href="#p1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err != nil {
		t.Fatalf("validateSVGContent: want nil for a local \"#id\" href, got %v", err)
	}
}

func TestValidateSVGContentRejectsDoctype(t *testing.T) {
	svg := "<?xml version=\"1.0\"?><!DOCTYPE svg [<!ENTITY x \"y\">]><svg xmlns=\"http://www.w3.org/2000/svg\">&x;</svg>"
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for DOCTYPE/ENTITY declaration, got nil")
	}
}

func TestValidateSVGContentRejectsNonSVGRoot(t *testing.T) {
	if err := validateSVGContent([]byte(`<html><body>not an svg</body></html>`)); err == nil {
		t.Fatal("validateSVGContent: want error for a non-<svg> root element, got nil")
	}
}

func TestValidateSVGContentRejectsMalformedXML(t *testing.T) {
	if err := validateSVGContent([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"</svg>`)); err == nil {
		t.Fatal("validateSVGContent: want error for malformed XML, got nil")
	}
}

func TestValidateSVGContentRejectsDisallowedAttribute(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect data-evil="x" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for an unlisted attribute, got nil")
	}
}

func TestValidateSVGContentRejectsAnimateElement(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"><animate attributeName="x" from="0" to="1"/></rect></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for <animate> (SMIL), got nil")
	}
}

// #626: fill/stroke/clip-path/mask can carry a CSS url(...) reference just
// like href — an external target here is a stored client-side fetch to an
// attacker-controlled or internal host whenever the asset is rendered, and
// was previously accepted verbatim because only href/xlink:href were
// value-checked.
func TestValidateSVGContentRejectsExternalURLInFill(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(http://attacker.example/x)" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for external url() in fill, got nil")
	}
}

func TestValidateSVGContentRejectsExternalURLInStroke(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect stroke="url(https://attacker.example/x)" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for external url() in stroke, got nil")
	}
}

func TestValidateSVGContentRejectsExternalURLInClipPath(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect clip-path="url(//attacker.example/x)" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for external url() in clip-path, got nil")
	}
}

func TestValidateSVGContentRejectsExternalURLInMask(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect mask="url(http://attacker.example/x)" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for external url() in mask, got nil")
	}
}

// A naive "trim the first url( / last )" check would wrongly accept this:
// the trimmed result ("#a) url(http://attacker.example/x") still starts
// with "#". validateSVGURLBearingValue must inspect every url(...)
// occurrence individually, not just the outermost span.
func TestValidateSVGContentRejectsDoubleURLBypassInFill(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(#a) url(http://attacker.example/x)" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for a local+external url() pair in fill, got nil")
	}
}

func TestValidateSVGContentRejectsCaseVariantURLScheme(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="URL(http://attacker.example/x)" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for URL( case variant in fill, got nil")
	}
}

func TestValidateSVGContentRejectsWhitespaceInsideURL(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(  http://attacker.example/x  )" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err == nil {
		t.Fatal("validateSVGContent: want error for whitespace-padded external url() in fill, got nil")
	}
}

func TestValidateSVGContentAcceptsLocalFragmentURLInFill(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
		<defs><linearGradient id="g1"><stop offset="0" stop-color="#fff"/></linearGradient></defs>
		<rect fill="url(#g1)" stroke="url('#g1')" width="1" height="1"/>
	</svg>`
	if err := validateSVGContent([]byte(svg)); err != nil {
		t.Fatalf("validateSVGContent: want nil for local fragment url() in fill/stroke, got %v", err)
	}
}

func TestValidateSVGContentAcceptsPlainColorInFill(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="#ff0000" stroke="none" width="1" height="1"/></svg>`
	if err := validateSVGContent([]byte(svg)); err != nil {
		t.Fatalf("validateSVGContent: want nil for a plain color value in fill/stroke, got %v", err)
	}
}
