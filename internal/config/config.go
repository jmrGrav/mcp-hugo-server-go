package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

type Config struct {
	// SiteRoot is the Hugo build-output directory (publishDir, normally
	// {HugoRoot}/public) that gets walked and indexed for content-page
	// discovery. It must NOT be set to the Hugo project root — a vendored
	// theme's raw .html layout templates under {HugoRoot}/themes/ would get
	// walked and mis-parsed as content pages (see index.go's project-root
	// warning).
	SiteRoot        string            `yaml:"site_root"`
	HugoRoot        string            `yaml:"hugo_root"`
	ContentRoot     string            `yaml:"content_root"`
	GitBaseline     GitBaselineConfig `yaml:"git_baseline"`
	SiteURL         string            `yaml:"site_url"`
	SiteName        string            `yaml:"site_name"`
	DefaultLanguage string            `yaml:"language_default"`
	// ConfiguredLanguages (#899) is an explicit, operator-declared list of
	// language codes the site supports — e.g. ["en", "fr"]. When set, it is
	// the authoritative source create_page uses to *reject* (not just warn
	// about) a lang value outside it, and get_capabilities.languages.available
	// reports exactly this set instead of the derived-from-content one, so
	// "advertised == enforced" holds. When unset/empty (the default), nothing
	// changes: create_page keeps its existing warn-not-reject behavior (#891)
	// and get_capabilities keeps deriving availability from indexed content —
	// this field is opt-in specifically so it never breaks an operator who
	// hasn't configured it. See unknownLangWarning's doc comment in
	// internal/tools/write/tools.go for why a reject rule needs this
	// operator-supplied set: the server has no other way to distinguish "not
	// configured" from "configured but has no content yet" (the latter must
	// still be accepted, or the first page of every new language deadlocks).
	ConfiguredLanguages []string `yaml:"configured_languages"`
	Transport           string   `yaml:"transport"`
	HTTPBindAddr        string   `yaml:"http_bind_addr"`
	HTTPBindPort        int      `yaml:"http_bind_port"`
	StreamingEnabled    bool     `yaml:"streaming_enabled"`
	MaxIndexEntries     int      `yaml:"max_index_entries"`
	MaxResultItems      int      `yaml:"max_result_items"`
	MaxRequestBytes     int64    `yaml:"max_request_bytes"`
	RejectSymlinks      bool     `yaml:"reject_symlinks"`
	RejectHiddenPath    bool     `yaml:"reject_hidden_paths"`
	// TechnicalVerificationSlugs (#1186) is an explicit, operator-declared
	// allowlist of single-segment root slugs (e.g. "abuseipdb-verification"
	// for static/abuseipdb-verification.html) that are third-party
	// domain-ownership-verification artifacts rather than content pages —
	// the same category robots.txt/security.txt already get, but those are
	// hardcoded because their names are fixed by spec while a verification
	// token's filename is assigned by the third party and varies per site.
	// Deliberately opt-in and never inferred from the slug/filename itself
	// (e.g. matching on "verification"): a legitimate content page that
	// happens to share a naming pattern must never be silently
	// reclassified. Listed slugs are excluded from IsContent() (so they
	// stop being counted as content pages missing a title/meta/canonical in
	// rendered_seo_summary and friends) but remain fully visible to
	// list_pages/validate_site and are still scanned for render-safety —
	// this only changes SEO-content bucketing, nothing else.
	TechnicalVerificationSlugs []string          `yaml:"technical_verification_slugs"`
	ImageGenURL                string            `yaml:"image_gen_url"`
	ImageGenKey                string            `yaml:"image_gen_key"`
	BuildTimeoutSeconds        int               `yaml:"build_timeout_seconds"`
	PostBuildHooks             []string          `yaml:"post_build_hooks"`
	TaxonomyAliases            map[string]string `yaml:"taxonomy_aliases"`
	SecurityContact            string            `yaml:"security_contact"`
	DBPath                     string            `yaml:"db_path"`
	Cloudflare                 CloudflareConfig  `yaml:"cloudflare"`
	IndexNow                   IndexNowConfig    `yaml:"indexnow"`
	GoogleIndex                GoogleIndexConfig `yaml:"google_indexing"`
	OAuth                      OAuthConfig       `yaml:"oauth"`
	RateLimit                  RateLimitConfig   `yaml:"rate_limit"`
	// IdempotencyTTLSeconds (#616) is the retention window for the
	// idempotency-key store backing create_page/update_page/delete_page/
	// upload_page_asset/delete_page_asset and the get_mutation_status lookup
	// (#586). It is deliberately a deployment-level setting rather than a
	// per-call parameter: a caller-supplied TTL could otherwise be shortened
	// to evade idempotency/duplicate-submission protection. A longer window
	// gives an agent more time to positively confirm via get_mutation_status
	// whether a mutation landed after a connector-level outage, instead of
	// falling back to a blind (if always-safe) retry. Defaults to 900 (15
	// minutes), the previously-hardcoded value.
	IdempotencyTTLSeconds int `yaml:"idempotency_ttl_seconds"`
	// BlockedShortcodes (#590) names Hugo shortcodes that create_page/update_page
	// refuse to accept in a page body, matched against the site's own
	// {{< name >}}/{{% name %}} invocation syntax. This is a best-effort
	// denylist, not a guarantee the theme's shortcode surface is fully safe:
	// it was seeded from an audit of the production LoveIt theme this server
	// was tested against (grep for safeHTML/safeJS/safeURL/safeCSS/Scratch
	// usage across its layouts/_shortcodes, followed by manual review of each
	// hit), not a general static analysis of arbitrary themes. That audit
	// found "raw" (stores .Inner for later unescaped DOM injection), "script"
	// (emits .Inner as a literal <script> tag), and "style" (emits its first
	// argument unescaped into a <style> rule — CSS injection, not script
	// execution, but still author-uncontrolled markup) as genuine bypasses of
	// markup.goldmark.renderer.unsafe=false, which otherwise already blocks
	// raw HTML typed directly into Markdown. The same audit's other hits
	// (echarts/image/mapbox/music/typeit) were reviewed and excluded: each
	// either routes its content through .Page.RenderString (already
	// markdown-safe) before any safeHTML pipe, or places .Get values only in
	// an HTML attribute context Hugo's shortcode templates (html/template)
	// already auto-escape. Operators must review their own theme's
	// shortcodes and extend this list for anything with an equivalent
	// escape hatch under a different name — this list does not, by itself,
	// make arbitrary agent-authored body content safe against every theme.
	BlockedShortcodes []string `yaml:"blocked_shortcodes"`
	// ForceDryRunAll (#611), when true, overrides every mutation tool's
	// per-call `dry_run` argument to true server-wide — create_page,
	// update_page, delete_page, upload_page_asset, delete_page_asset,
	// apply_content_plan, and rollback_change all become read-only previews
	// regardless of what a caller passes. Intended for safely exercising the
	// full write-tool surface during a live audit or CI smoke run without
	// every caller having to remember `dry_run: true` on every call, and
	// without touching rate-limit quota (dry-run calls already don't consume
	// it). Deliberately a single server-wide config flag, not a per-caller/
	// per-session mechanism — matches this repo's existing preference for
	// the simplest mechanism proportionate to the actual use case (avoiding
	// new OAuth/session plumbing for what safe-audit/CI use cases need).
	// Each affected tool's response still reports `data.dry_run: true` as
	// normal, so the override is directly visible to the caller, not silent.
	ForceDryRunAll bool `yaml:"force_dry_run_all"`
	// StaleTestContentThresholdHours (#608) is the age (in hours) past which
	// a still-published page whose slug matches contentmodel's reserved
	// test-content prefix convention (mcp-audit-/test-audit-/codex-, #584)
	// triggers a post-build advisory warning. validate_frontmatter/
	// validate_site's test_content_slugs already *detects* this on demand;
	// this check goes one step further by surfacing it proactively on every
	// build_site/publish_changes, so a forgotten test page doesn't require
	// an operator to think to ask. Report-only: it never deletes or
	// modifies anything, matching this repo's existing "advisory, not
	// automatic remediation" posture for destructive-adjacent signals
	// (get_broken_links, get_site_health). Off by default (0): an operator
	// opts in by setting a positive value (e.g. 24 for one day), so
	// upgrading to a version with this field never silently changes an
	// existing deployment's behavior or log volume.
	StaleTestContentThresholdHours int `yaml:"stale_test_content_threshold_hours"`
	// PreviewMaxPerCaller (#871) caps how many active previews a single caller
	// may hold open at once, enforced in create_preview before a build starts.
	// It bounds the number of isolated Hugo build directories an agent can
	// accumulate under the system temp dir. NOTE on granularity: "caller" here
	// is the preview's Owner, which is the server's OS process user
	// (currentUserForLog), not a per-OAuth-client identity — in the common
	// single-process deployment this is effectively a global active-preview
	// cap, and is documented as such rather than overclaiming per-client
	// isolation. Defaults to 5. A non-positive value is treated as
	// "unset -> default" (see clampPreviewLimits), never as "unlimited", so an
	// upgrade or a config typo can't silently remove the cap.
	PreviewMaxPerCaller int `yaml:"preview_max_per_caller"`
	// PreviewMaxDiskBytes (#871) is a *global* cap on the total on-disk size of
	// all active preview build directories, checked in create_preview before a
	// build. Global (not per-caller) is deliberate: disk is a single shared
	// physical resource and Owner attribution is best-effort OS-user (see
	// above), so the meaningful protection against filling the disk is one
	// aggregate budget. Once current usage is at/over the cap, new previews are
	// refused with an explicit error until existing ones expire or are revoked.
	// Defaults to 512 MiB. Non-positive -> default (clampPreviewLimits), so the
	// disk protection can't be silently disabled by a zeroed/omitted value.
	PreviewMaxDiskBytes int64 `yaml:"preview_max_disk_bytes"`
	// PreviewExternalVerification makes create_preview verify its returned URL
	// through the configured public OAuth issuer before reporting success. The
	// probe follows the real entry-token/cookie flow and compares a nested HTML
	// page plus an asset with the isolated build, so a reverse-proxy homepage
	// fallback cannot pass merely because it returned HTTP 200. Disabled by
	// default because it deliberately makes a bounded outbound request to the
	// operator-configured issuer.
	PreviewExternalVerification bool `yaml:"preview_external_verification"`
	// HugoUpgrade configures the optional, operator-controlled Hugo binary
	// lifecycle. Status checks remain read-only; staging/activation/rollback
	// are disabled unless Enabled is true and every managed path is explicit.
	HugoUpgrade HugoUpgradeConfig `yaml:"hugo_upgrade"`
}

// HugoUpgradeConfig constrains managed Hugo upgrades to one private directory
// and one symlink inside it. The server never replaces a package-manager-owned
// binary or restarts itself.
type HugoUpgradeConfig struct {
	Enabled           bool     `yaml:"enabled"`
	ManagedDir        string   `yaml:"managed_dir"`
	BinaryLink        string   `yaml:"binary_link"`
	ReleaseAPIBaseURL string   `yaml:"release_api_base_url"`
	AllowedHosts      []string `yaml:"allowed_hosts"`
	MaxDownloadBytes  int64    `yaml:"max_download_bytes"`
	CacheTTLSeconds   int      `yaml:"cache_ttl_seconds"`
	RequireExtended   bool     `yaml:"require_extended"`
	AllowDowngrade    bool     `yaml:"allow_downgrade"`
	MinimumVersion    string   `yaml:"minimum_version"`
	MaximumVersion    string   `yaml:"maximum_version"`
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
		// http, not stdio: until #782's stdio transport, this field was
		// validated but never actually branched on anywhere, so every
		// existing deployment has always run HTTP regardless of what this
		// default said. Defaulting to "http" preserves that real,
		// already-observed behavior for any config.yaml that omits
		// transport entirely; explicit transport: stdio still opts in.
		Transport:        "http",
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
		BlockedShortcodes:     []string{"raw", "rawhtml", "script", "style"},
		IdempotencyTTLSeconds: DefaultIdempotencyTTLSeconds,
		PreviewMaxPerCaller:   DefaultPreviewMaxPerCaller,
		PreviewMaxDiskBytes:   DefaultPreviewMaxDiskBytes,
		HugoUpgrade: HugoUpgradeConfig{
			ReleaseAPIBaseURL: "https://api.github.com/repos/gohugoio/hugo",
			AllowedHosts:      []string{"api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"},
			MaxDownloadBytes:  128 * 1024 * 1024,
			CacheTTLSeconds:   3600,
			RequireExtended:   true,
		},
	}
}

// DefaultPreviewMaxPerCaller and DefaultPreviewMaxDiskBytes are the fail-safe
// values for the create_preview active-count and preview-disk caps (#871),
// applied when the config value is unset or non-positive. Exported so the
// admin layer can single-source the same defensive fallback.
const (
	DefaultPreviewMaxPerCaller = 5
	DefaultPreviewMaxDiskBytes = 512 * 1024 * 1024 // 512 MiB
)

// DefaultIdempotencyTTLSeconds is the retention window (in seconds) used when
// idempotency_ttl_seconds is unset or configured to an invalid (non-positive)
// value. Matches the previously-hardcoded 15-minute TTL (#616). Exported so
// internal/tools/write can single-source its own defensive fallback rather
// than duplicating the value.
const DefaultIdempotencyTTLSeconds = 15 * 60

func Load(path string) (Config, error) {
	cfg := Default()
	if strings.TrimSpace(path) != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config: %w", err)
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return Config{}, fmt.Errorf("config: %w", err)
		}
	}
	// applyEnvOverlay runs for both the with-file and no-file cases: an MCPB
	// stdio install has no config.yaml at all and delivers user_config
	// (site_root/hugo_root/etc) purely via environment variables (the MCPB
	// manifest spec's ${user_config.*} substitution only injects into
	// env/args — it cannot write a file). See config_env_overlay.go.
	applyEnvOverlay(&cfg)
	if cfg.Transport != "stdio" && cfg.Transport != "http" {
		return Config{}, fmt.Errorf("config: invalid transport %q", cfg.Transport)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	cfg.RateLimit.clampMutationLimits()
	cfg.clampIdempotencyTTL()
	cfg.clampPreviewLimits()
	return cfg, nil
}

// clampPreviewLimits guards the create_preview caps (#871) against a config
// file zeroing or negating them: a non-positive count or disk cap would
// otherwise be read as "no limit", silently reintroducing the unbounded
// preview-accumulation / disk-exhaustion risk this feature exists to prevent.
// Fall back to the safe Default() values instead of failing closed, mirroring
// clampIdempotencyTTL / clampMutationLimits' treatment of the same class of
// misconfiguration.
func (c *Config) clampPreviewLimits() {
	if c.PreviewMaxPerCaller <= 0 {
		c.PreviewMaxPerCaller = DefaultPreviewMaxPerCaller
	}
	if c.PreviewMaxDiskBytes <= 0 {
		c.PreviewMaxDiskBytes = DefaultPreviewMaxDiskBytes
	}
}

// clampIdempotencyTTL guards against a config file zeroing or negating the
// idempotency-key retention window: a non-positive TTL would make every
// mutation call effectively non-idempotent (no replay protection window),
// silently reintroducing duplicate-submission risk. Fall back to the safe
// Default() value instead of failing closed, mirroring
// RateLimitConfig.clampMutationLimits' treatment of the same class of
// misconfiguration (#616).
func (c *Config) clampIdempotencyTTL() {
	if c.IdempotencyTTLSeconds <= 0 {
		c.IdempotencyTTLSeconds = DefaultIdempotencyTTLSeconds
	}
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
	if err := c.validateHugoUpgrade(); err != nil {
		return err
	}
	return nil
}

func (c *Config) validateHugoUpgrade() error {
	h := &c.HugoUpgrade
	if strings.TrimSpace(h.ReleaseAPIBaseURL) == "" {
		h.ReleaseAPIBaseURL = "https://api.github.com/repos/gohugoio/hugo"
	}
	if h.MaxDownloadBytes <= 0 {
		h.MaxDownloadBytes = 128 * 1024 * 1024
	}
	if h.CacheTTLSeconds <= 0 {
		h.CacheTTLSeconds = 3600
	}
	if len(h.AllowedHosts) == 0 {
		h.AllowedHosts = []string{"api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com"}
	}
	u, err := url.Parse(h.ReleaseAPIBaseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("config: hugo_upgrade.release_api_base_url must be an absolute HTTPS URL")
	}
	if !hostAllowed(u.Hostname(), h.AllowedHosts) {
		return fmt.Errorf("config: hugo_upgrade.release_api_base_url host is not in allowed_hosts")
	}
	if !h.Enabled {
		return nil
	}
	for name, version := range map[string]string{"minimum_version": h.MinimumVersion, "maximum_version": h.MaximumVersion} {
		if strings.TrimSpace(version) != "" && !validStableHugoVersion(version) {
			return fmt.Errorf("config: hugo_upgrade.%s must be an exact stable vMAJOR.MINOR.PATCH version", name)
		}
	}
	if h.MinimumVersion != "" && h.MaximumVersion != "" && compareHugoVersions(h.MinimumVersion, h.MaximumVersion) > 0 {
		return fmt.Errorf("config: hugo_upgrade.minimum_version must not exceed maximum_version")
	}
	if !filepath.IsAbs(h.ManagedDir) || !filepath.IsAbs(h.BinaryLink) {
		return fmt.Errorf("config: hugo_upgrade managed_dir and binary_link must be absolute when enabled")
	}
	managed := filepath.Clean(h.ManagedDir)
	link := filepath.Clean(h.BinaryLink)
	rel, err := filepath.Rel(managed, link)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("config: hugo_upgrade.binary_link must be inside managed_dir")
	}
	return nil
}

func validStableHugoVersion(version string) bool {
	version = strings.TrimSpace(version)
	return semver.IsValid(version) && semver.Prerelease(version) == "" && semver.Build(version) == ""
}

func compareHugoVersions(a, b string) int {
	return semver.Compare(a, b)
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, candidate := range allowed {
		if host == strings.ToLower(strings.TrimSuffix(strings.TrimSpace(candidate), ".")) {
			return true
		}
	}
	return false
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
