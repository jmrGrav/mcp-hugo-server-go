package oauth

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func validateRegisteredRedirectURI(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("invalid redirect_uri")
	}
	if strings.Count(raw, "*") > 1 {
		return fmt.Errorf("invalid redirect_uri")
	}
	if strings.Contains(raw, "*") && !strings.HasSuffix(raw, "*") {
		return fmt.Errorf("invalid redirect_uri")
	}
	if strings.HasSuffix(raw, "*") {
		raw = strings.TrimSuffix(raw, "*")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid redirect_uri")
	}
	switch u.Scheme {
	case "https":
		if strings.Contains(u.Host, "*") {
			return fmt.Errorf("invalid redirect_uri")
		}
		if strings.HasSuffix(raw, "/") {
			return nil
		}
		return nil
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return fmt.Errorf("invalid redirect_uri")
		}
		return nil
	default:
		return fmt.Errorf("invalid redirect_uri")
	}
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func matchRedirectURI(registered, actual string) bool {
	registered = strings.TrimSpace(registered)
	actual = strings.TrimSpace(actual)
	if registered == "" || actual == "" {
		return false
	}

	wildcard := strings.HasSuffix(registered, "*")
	if wildcard {
		registered = strings.TrimSuffix(registered, "*")
	}

	regURL, err := url.Parse(registered)
	if err != nil || regURL.Scheme == "" || regURL.Host == "" {
		return false
	}
	actURL, err := url.Parse(actual)
	if err != nil || actURL.Scheme == "" || actURL.Host == "" {
		return false
	}
	if regURL.Scheme != actURL.Scheme || !strings.EqualFold(regURL.Host, actURL.Host) {
		return false
	}

	if wildcard {
		if regURL.Scheme != "https" {
			return false
		}
		prefix := regURL.Path
		if prefix == "" {
			prefix = "/"
		}
		if !strings.HasPrefix(actURL.Path, prefix) {
			return false
		}
		if regURL.RawQuery != "" && regURL.RawQuery != actURL.RawQuery {
			return false
		}
		return true
	}

	return normalizeRedirectURL(regURL) == normalizeRedirectURL(actURL)
}

func normalizeRedirectURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + u.Path + querySuffix(u.RawQuery) + fragmentSuffix(u.Fragment)
}

func querySuffix(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	return "?" + rawQuery
}

func fragmentSuffix(fragment string) string {
	if fragment == "" {
		return ""
	}
	return "#" + fragment
}
