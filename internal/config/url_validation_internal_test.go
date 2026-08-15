package config

import "testing"

// TestValidateExternalURLRejectsPrivateAndLinkLocalIPs is a security-boundary
// regression test that had zero coverage: validateExternalURL/
// isPrivateOrLinkLocal gate post_build_hooks and image_gen_url, both
// outbound-request-triggering config fields — this is this server's SSRF
// guard for literal-IP targets. Table-driven across every CIDR block the
// implementation claims to cover (RFC 1918 private ranges, loopback,
// link-local v4/v6, unique-local v6), so a future edit that narrows one of
// those ranges fails a specific row instead of a vague aggregate check.
func TestValidateExternalURLRejectsPrivateAndLinkLocalIPs(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"RFC1918 10.0.0.0/8", "http://10.1.2.3/hook"},
		{"RFC1918 172.16.0.0/12", "http://172.20.0.1/hook"},
		{"RFC1918 192.168.0.0/16", "http://192.168.1.1/hook"},
		{"loopback v4", "http://127.0.0.1/hook"},
		{"loopback v6", "http://[::1]/hook"},
		{"unique-local v6 fc00::/7", "http://[fc00::1]/hook"},
		{"link-local v4 169.254.0.0/16", "http://169.254.169.254/hook"}, // cloud metadata endpoint
		{"link-local v6 fe80::/10", "http://[fe80::1]/hook"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateExternalURL(tc.url); err == nil {
				t.Fatalf("validateExternalURL(%q) = nil, want a rejection error", tc.url)
			}
		})
	}
}

// TestValidateExternalURLRejectsNonHTTPSchemes covers the scheme allowlist:
// only http/https are ever accepted.
func TestValidateExternalURLRejectsNonHTTPSchemes(t *testing.T) {
	cases := []string{
		"ftp://example.com/hook",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"gopher://example.com/hook",
		"not-a-url",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if err := validateExternalURL(raw); err == nil {
				t.Fatalf("validateExternalURL(%q) = nil, want a rejection error", raw)
			}
		})
	}
}

// TestValidateExternalURLAcceptsPublicIPsAndHostnames documents the
// deliberate, already-commented design boundary (issue #112): hostname-based
// URLs are accepted without DNS resolution, so this check only ever catches
// a *literal* private/link-local IP in the URL, not a hostname that resolves
// to one at request time (DNS rebinding is out of scope for this
// config-load-time check). A public literal IP is accepted either way.
func TestValidateExternalURLAcceptsPublicIPsAndHostnames(t *testing.T) {
	cases := []string{
		"http://8.8.8.8/hook",
		"https://example.com/hook",
		"https://internal.corp.example.com/hook", // hostname form: not resolved, always accepted here
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			if err := validateExternalURL(raw); err != nil {
				t.Fatalf("validateExternalURL(%q) = %v, want accepted", raw, err)
			}
		})
	}
}

func TestValidateExternalURLRejectsMissingHost(t *testing.T) {
	if err := validateExternalURL("http:///hook"); err == nil {
		t.Fatal("validateExternalURL with empty host = nil, want a rejection error")
	}
}
