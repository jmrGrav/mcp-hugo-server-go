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
	SiteRoot            string            `yaml:"site_root"`
	HugoRoot            string            `yaml:"hugo_root"`
	ContentRoot         string            `yaml:"content_root"`
	GitBaseline         GitBaselineConfig `yaml:"git_baseline"`
	SiteURL             string            `yaml:"site_url"`
	SiteName            string            `yaml:"site_name"`
	DefaultLanguage     string            `yaml:"language_default"`
	Transport           string            `yaml:"transport"`
	HTTPBindAddr        string            `yaml:"http_bind_addr"`
	HTTPBindPort        int               `yaml:"http_bind_port"`
	StreamingEnabled    bool              `yaml:"streaming_enabled"`
	MaxIndexEntries     int               `yaml:"max_index_entries"`
	MaxResultItems      int               `yaml:"max_result_items"`
	MaxRequestBytes     int64             `yaml:"max_request_bytes"`
	RejectSymlinks      bool              `yaml:"reject_symlinks"`
	RejectHiddenPath    bool              `yaml:"reject_hidden_paths"`
	ImageGenURL         string            `yaml:"image_gen_url"`
	ImageGenKey         string            `yaml:"image_gen_key"`
	BuildTimeoutSeconds int               `yaml:"build_timeout_seconds"`
	PostBuildHooks      []string          `yaml:"post_build_hooks"`
	TaxonomyAliases     map[string]string `yaml:"taxonomy_aliases"`
	SecurityContact     string            `yaml:"security_contact"`
	DBPath              string            `yaml:"db_path"`
	Cloudflare          CloudflareConfig  `yaml:"cloudflare"`
	IndexNow            IndexNowConfig    `yaml:"indexnow"`
	GoogleIndex         GoogleIndexConfig `yaml:"google_indexing"`
	OAuth               OAuthConfig       `yaml:"oauth"`
	RateLimit           RateLimitConfig   `yaml:"rate_limit"`
}

// GitBaselineConfig defines the local Git checkout model used as the trusted
// baseline for diff/runtime diagnostics. It is intentionally configuration-only
// at this stage: runtime consumers can adopt it incrementally without guessing
// paths or remotes from host layout.
type GitBaselineConfig struct {
	Mode     string `yaml:"mode"`      // auto, configured, or disabled
	RepoPath string `yaml:"repo_path"` // absolute local checkout path when mode=configured
	Branch   string `yaml:"branch"`    // expected branch name for diagnostics
	Remote   string `yaml:"remote"`    // expected remote name for diagnostics
}

// CloudflareConfig holds credentials for Cloudflare cache purge. Zero value
// disables all purge calls (no-op). Never commit api_token to version control —
// set it only in the server config file on the host.
type CloudflareConfig struct {
	ZoneID   string `yaml:"zone_id"`
	APIToken string `yaml:"api_token"`
}

func (c CloudflareConfig) Enabled() bool {
	return c.ZoneID != "" && c.APIToken != ""
}

// IndexNowConfig holds the IndexNow API key and optional submission endpoint.
type IndexNowConfig struct {
	Key         string `yaml:"key"`
	KeyLocation string `yaml:"key_location"` // full URL to the key verification file
	Host        string `yaml:"host"`
	Endpoint    string `yaml:"endpoint"` // defaults to https://api.indexnow.org/indexnow
}

func (c IndexNowConfig) Enabled() bool { return c.Key != "" }

// GoogleIndexConfig holds credentials for the Google Indexing API v3.
// ServiceAccountPath must point to a service account JSON file on the host.
// Never commit the JSON to version control.
type GoogleIndexConfig struct {
	ServiceAccountPath string `yaml:"service_account_path"`
	DailyQuotaLimit    int    `yaml:"daily_quota_limit"` // default 180
	QuotaStatePath     string `yaml:"quota_state_path"`  // default /var/lib/mcp-hugo-server-go/google-index-quota.json
}

func (c GoogleIndexConfig) Enabled() bool { return c.ServiceAccountPath != "" }

type RateLimitConfig struct {
	AnonymousPerMin    int `yaml:"anonymous_per_min"`
	ContentReadPerMin  int `yaml:"content_read_per_min"`
	ContentWritePerMin int `yaml:"content_write_per_min"`
	SiteAdminPerMin    int `yaml:"site_admin_per_min"` // HTTP requests/min; effective tool calls ≈ N/2 in stateful mode
	DestructivePerMin  int `yaml:"destructive_per_min"`
	// CreateUpdatePerMin bounds create_page/update_page/upload_page_asset
	// per caller (#378), the same defense-in-depth pattern already applied
	// to delete_page via DestructivePerMin — layered on top of, not instead
	// of, the broader per-scope-per-IP limit content.write already gets
	// from internal/oauth's RateLimiter. That existing limiter is a single
	// shared budget across every content.write tool; this one gives
	// create/update/upload their own per-caller budget the same way delete
	// already has its own, so one operation type can't silently consume
	// another's headroom.
	CreateUpdatePerMin int `yaml:"create_update_per_min"`
}

// OAuthConfig holds OAuth 2.0 server configuration.
// StorageBackend selects the token persistence backend: "memory" (default),
// "json", or "sqlite". StoragePath is required for json and sqlite backends.
// Access tokens are persisted via the backend; in-Service state (clients,
// auth codes, agent registrations) is intentionally ephemeral and resets on
// restart. See issue #26 for the rationale.
type OAuthConfig struct {
	Enabled                     bool     `yaml:"enabled"`
	Issuer                      string   `yaml:"issuer"`
	Resource                    string   `yaml:"resource"`
	DynamicClientEnabled        bool     `yaml:"dynamic_client_registration"`
	AllowReaderSelfRegistration bool     `yaml:"allow_reader_self_registration"`
	ClientRegistryPath          string   `yaml:"client_registry_path"`
	RequirePKCE                 bool     `yaml:"require_pkce"`
	TrustedAuthorizeCIDRs       []string `yaml:"trusted_authorize_cidrs"`
	AuthCodeTTLSeconds          int      `yaml:"auth_code_ttl_seconds"`
	AccessTokenTTLSeconds       int      `yaml:"access_token_ttl_seconds"`
	RefreshTokenTTLSeconds      int      `yaml:"refresh_token_ttl_seconds"`
	StorageBackend              string   `yaml:"storage_backend"`
	StoragePath                 string   `yaml:"storage_path"`
}

func Default() Config {
	return Config{
		Transport:        "stdio",
		HTTPBindAddr:     "127.0.0.1",
		HTTPBindPort:     8088,
		StreamingEnabled: true,
		DefaultLanguage:  "en",
		GitBaseline: GitBaselineConfig{
			Mode:   "auto",
			Branch: "main",
			Remote: "origin",
		},
		MaxIndexEntries:     5000,
		MaxResultItems:      50,
		MaxRequestBytes:     1 << 20,
		RejectSymlinks:      true,
		RejectHiddenPath:    true,
		BuildTimeoutSeconds: 120,
		// MCP rate limiting is enforced on logical tools/call requests rather than
		// Streamable HTTP session-control traffic.
		RateLimit: RateLimitConfig{
			AnonymousPerMin:    120,
			ContentReadPerMin:  240,
			ContentWritePerMin: 60,
			SiteAdminPerMin:    60,
			DestructivePerMin:  5,
			CreateUpdatePerMin: 60,
		},
		OAuth: OAuthConfig{
			RequirePKCE: true,
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
	cfg.RateLimit.clampMutationLimits()
	return cfg, nil
}

// clampMutationLimits guards against a config file zeroing or negating the
// per-caller limits on destructive/mutating write tools (delete_page,
// create_page/update_page/upload_page_asset). callerLimiter fails open when
// given a non-positive per-minute budget, so a config typo (e.g.
// destructive_per_min: 0) must not be allowed to silently disable rate
// limiting on these operations; fall back to the safe Default() values
// instead.
func (r *RateLimitConfig) clampMutationLimits() {
	if r.DestructivePerMin <= 0 {
		r.DestructivePerMin = 5
	}
	if r.CreateUpdatePerMin <= 0 {
		r.CreateUpdatePerMin = 60
	}
}

// validate performs fail-fast checks on cross-field invariants.
func (c *Config) validate() error {
	switch c.GitBaseline.Mode {
	case "", "auto", "configured", "disabled":
	default:
		return fmt.Errorf("config: git_baseline.mode %q must be one of auto, configured, disabled", c.GitBaseline.Mode)
	}
	if c.GitBaseline.Mode == "configured" {
		if strings.TrimSpace(c.GitBaseline.RepoPath) == "" {
			return fmt.Errorf("config: git_baseline.repo_path is required when git_baseline.mode is configured")
		}
		if !strings.HasPrefix(c.GitBaseline.RepoPath, "/") {
			return fmt.Errorf("config: git_baseline.repo_path must be an absolute path")
		}
	}
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
