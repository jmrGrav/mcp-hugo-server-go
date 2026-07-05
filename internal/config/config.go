package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SiteRoot            string          `yaml:"site_root"`
	HugoRoot            string          `yaml:"hugo_root"`
	ContentRoot         string          `yaml:"content_root"`
	SiteURL             string          `yaml:"site_url"`
	SiteName            string          `yaml:"site_name"`
	DefaultLanguage     string          `yaml:"language_default"`
	Transport           string          `yaml:"transport"`
	HTTPBindAddr        string          `yaml:"http_bind_addr"`
	HTTPBindPort        int             `yaml:"http_bind_port"`
	StreamingEnabled    bool            `yaml:"streaming_enabled"`
	MaxIndexEntries     int             `yaml:"max_index_entries"`
	MaxResultItems      int             `yaml:"max_result_items"`
	MaxRequestBytes     int64           `yaml:"max_request_bytes"`
	RejectSymlinks      bool            `yaml:"reject_symlinks"`
	RejectHiddenPath    bool            `yaml:"reject_hidden_paths"`
	ImageGenURL         string          `yaml:"image_gen_url"`
	ImageGenKey         string          `yaml:"image_gen_key"`
	BuildTimeoutSeconds int             `yaml:"build_timeout_seconds"`
	PostBuildHooks      []string        `yaml:"post_build_hooks"`
	SecurityContact     string          `yaml:"security_contact"`
	OAuth               OAuthConfig     `yaml:"oauth"`
	RateLimit           RateLimitConfig `yaml:"rate_limit"`
}

type RateLimitConfig struct {
	AnonymousPerMin    int `yaml:"anonymous_per_min"`
	ContentReadPerMin  int `yaml:"content_read_per_min"`
	ContentWritePerMin int `yaml:"content_write_per_min"`
	SiteAdminPerMin    int `yaml:"site_admin_per_min"`
	DestructivePerMin  int `yaml:"destructive_per_min"`
}

// OAuthConfig holds OAuth 2.0 server configuration.
// StorageBackend selects the token persistence backend: "memory" (default),
// "json", or "sqlite". StoragePath is required for json and sqlite backends.
// Access tokens are persisted via the backend; in-Service state (clients,
// auth codes, agent registrations) is intentionally ephemeral and resets on
// restart. See issue #26 for the rationale.
type OAuthConfig struct {
	Enabled               bool     `yaml:"enabled"`
	Issuer                string   `yaml:"issuer"`
	Resource              string   `yaml:"resource"`
	DynamicClientEnabled  bool     `yaml:"dynamic_client_registration"`
	ClientRegistryPath    string   `yaml:"client_registry_path"`
	RequirePKCE           bool     `yaml:"require_pkce"`
	TrustedAuthorizeCIDRs []string `yaml:"trusted_authorize_cidrs"`
	AuthCodeTTLSeconds    int      `yaml:"auth_code_ttl_seconds"`
	AccessTokenTTLSeconds int      `yaml:"access_token_ttl_seconds"`
	StorageBackend        string   `yaml:"storage_backend"`
	StoragePath           string   `yaml:"storage_path"`
}

func Default() Config {
	return Config{
		Transport:           "stdio",
		HTTPBindAddr:        "127.0.0.1",
		HTTPBindPort:        8088,
		StreamingEnabled:    true,
		DefaultLanguage:     "en",
		MaxIndexEntries:     5000,
		MaxResultItems:      50,
		MaxRequestBytes:     1 << 20,
		RejectSymlinks:      true,
		RejectHiddenPath:    true,
		BuildTimeoutSeconds: 120,
		RateLimit: RateLimitConfig{
			AnonymousPerMin:    60,
			ContentReadPerMin:  120,
			ContentWritePerMin: 30,
			SiteAdminPerMin:    10,
			DestructivePerMin:  5,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	if cfg.Transport != "stdio" && cfg.Transport != "http" {
		return Config{}, fmt.Errorf("config: invalid transport %q", cfg.Transport)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate performs fail-fast checks on cross-field invariants.
func (c *Config) validate() error {
	if c.OAuth.Enabled {
		if strings.TrimSpace(c.OAuth.Issuer) == "" {
			return fmt.Errorf("config: oauth.issuer is required when oauth.enabled is true")
		}
	}
	for _, hookURL := range c.PostBuildHooks {
		if err := validateHookURL(hookURL); err != nil {
			return fmt.Errorf("config: post_build_hooks: %w", err)
		}
	}
	if c.ImageGenURL != "" {
		if err := validateExternalURL(c.ImageGenURL); err != nil {
			return fmt.Errorf("config: image_gen_url: %w", err)
		}
	}
	return nil
}

// validateHookURL rejects non-HTTP(S) schemes and private/link-local IP ranges.
func validateHookURL(raw string) error {
	return validateExternalURL(raw)
}

// validateExternalURL rejects non-HTTP(S) schemes and literal private/link-local
// IP addresses. Hostname-based URLs are accepted without DNS resolution to avoid
// side effects at config load time (issue #112).
func validateExternalURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL %q: only http/https schemes are allowed", raw)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL %q: missing host", raw)
	}
	if ip := net.ParseIP(host); ip != nil && isPrivateOrLinkLocal(ip) {
		return fmt.Errorf("URL %q: private/link-local IP addresses are not allowed", raw)
	}
	return nil
}

func isPrivateOrLinkLocal(ip net.IP) bool {
	private4 := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8"}
	private6 := []string{"::1/128", "fc00::/7"}
	linkLocal := []string{"169.254.0.0/16", "fe80::/10"}
	all := append(append(private4, private6...), linkLocal...)
	for _, cidr := range all {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
