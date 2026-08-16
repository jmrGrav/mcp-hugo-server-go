package anonymous

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	adminpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	writepkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/write"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// get_capabilities (#859) is a machine-readable runtime capability surface:
// version/commit, languages, hard limits, allowed asset formats, blocked
// shortcodes, and coarse feature-availability flags — so an agent can plan
// deterministically instead of scraping prose tool descriptions or probing
// limits by triggering failures. It is anonymous-tier so discovery works
// pre-auth. It reports only NON-sensitive values: booleans/counts for
// configured integrations, never secrets (API keys), never hook command
// strings, never host paths.

type getCapabilitiesInput struct{}

type capabilitiesServer struct {
	ReleaseVersion string `json:"release_version"`
	Commit         string `json:"commit,omitempty"`
	BuildChannel   string `json:"build_channel,omitempty"`
	SchemaVersion  string `json:"schema_version"`
}

type capabilitiesLanguages struct {
	Default   string   `json:"default,omitempty"`
	Mode      string   `json:"mode"`
	Available []string `json:"available"`
}

type capabilitiesPreviewTTL struct {
	MinSeconds     int `json:"min_seconds"`
	DefaultSeconds int `json:"default_seconds"`
	MaxSeconds     int `json:"max_seconds"`
}

type capabilitiesRateLimits struct {
	CreateUpdateUploadPerMin int `json:"create_update_upload_per_min"`
	DestructivePerMin        int `json:"destructive_per_min"`
}

type capabilitiesLimits struct {
	BodyMaxBytes           int                    `json:"body_max_bytes"`
	TitleMaxRunes          int                    `json:"title_max_runes"`
	AssetMaxBytes          int                    `json:"asset_max_bytes"`
	MaxRequestBytes        int64                  `json:"max_request_bytes,omitempty"`
	MaxResultItems         int                    `json:"max_result_items,omitempty"`
	TestContentMaxTTLHours int                    `json:"test_content_max_ttl_hours"`
	PreviewTTL             capabilitiesPreviewTTL `json:"preview_ttl"`
	RateLimits             capabilitiesRateLimits `json:"rate_limits"`
}

// capabilitiesFeatures reports coarse availability of optional integrations
// as booleans/counts only — deliberately not the configuration values
// themselves (URLs, keys, hook commands are secrets or host detail).
type capabilitiesFeatures struct {
	ImageGenerationAvailable         bool   `json:"image_generation_available"`
	LocalHeroGenerationAvailable     bool   `json:"local_hero_generation_available"`
	ExternalImageGenerationAvailable bool   `json:"external_image_generation_available"`
	PostBuildHooksConfigured         bool   `json:"post_build_hooks_configured"`
	PostBuildHooksCount              int    `json:"post_build_hooks_count"`
	StreamingEnabled                 bool   `json:"streaming_enabled"`
	OAuthEnabled                     bool   `json:"oauth_enabled"`
	ForceDryRunAll                   bool   `json:"force_dry_run_all"`
	CloudflarePurgeConfigured        bool   `json:"cloudflare_purge_configured"`
	IndexNowConfigured               bool   `json:"indexnow_configured"`
	GoogleIndexingConfigured         bool   `json:"google_indexing_configured"`
	GitBaselineMode                  string `json:"git_baseline_mode,omitempty"`
}

type getCapabilitiesData struct {
	EffectiveScopes     []string                      `json:"effective_scopes,omitempty"`
	MaskedTools         *capabilitiesMaskedTools      `json:"masked_tools,omitempty"`
	DisabledFeatures    []capabilitiesDisabledFeature `json:"disabled_features,omitempty"`
	Server              capabilitiesServer            `json:"server"`
	Languages           capabilitiesLanguages         `json:"languages"`
	Limits              capabilitiesLimits            `json:"limits"`
	AllowedImageFormats []string                      `json:"allowed_image_formats"`
	BlockedShortcodes   []string                      `json:"blocked_shortcodes"`
	Features            capabilitiesFeatures          `json:"features"`
}

type capabilitiesMaskedTools struct {
	Count  int      `json:"count"`
	Reason string   `json:"reason,omitempty"`
	Scopes []string `json:"required_scopes,omitempty"`
}

// capabilitiesDisabledFeature explains an optional integration disabled by
// configuration without exposing credentials or host paths.
type capabilitiesDisabledFeature struct {
	Name                  string `json:"name"`
	Reason                string `json:"reason"`
	RequiredConfiguration string `json:"required_configuration,omitempty"`
}

type getCapabilitiesOutput struct {
	toolcontract.ToolResponse[getCapabilitiesData]
}

func newGetCapabilitiesOutput(data getCapabilitiesData) getCapabilitiesOutput {
	meta := toolcontract.NewMeta(buildinfo.Version, time.Now().UTC())
	meta.ContentProvenance = contentProvenanceServerGeneratedTrusted
	return getCapabilitiesOutput{ToolResponse: toolcontract.Success(data, meta)}
}

// availableLanguages returns the distinct non-empty language codes present in
// the built index plus the configured default, sorted. Empty-string (the
// default/unlabelled bucket) is folded into the default, not listed as "".
//
// configuredLangs (#899), when non-empty, means the operator has declared an
// authoritative language set via config's configured_languages — the same
// set write.rejectUnconfiguredLang enforces create_page's lang param
// against. In that case this returns exactly that set (deduped, default
// folded in, sorted) instead of the derived-from-content one, so
// "advertised == enforced" holds per #899's acceptance criteria: an agent
// checking get_capabilities before calling create_page sees precisely the
// langs that will be accepted, including a configured-but-still-empty one
// that wouldn't otherwise appear until it had content. Empty
// configuredLangs (the default for every operator who hasn't set the field)
// preserves the original derived-from-content behavior unchanged.
func availableLanguages(idx *site.Index, srcIdx *hugosite.SourceIndex, defaultLang string, configuredLangs []string) []string {
	seen := map[string]bool{}
	if d := strings.TrimSpace(defaultLang); d != "" {
		seen[d] = true
	}
	if len(configuredLangs) > 0 {
		for _, c := range configuredLangs {
			if c = strings.TrimSpace(c); c != "" {
				seen[c] = true
			}
		}
	} else {
		// Source is authoritative for observed languages. A failed or partial
		// build can leave the public index with only one language; deriving
		// capabilities from that tree would make an agent believe the site had
		// become monolingual precisely while it most needs accurate recovery
		// information.
		if srcIdx != nil {
			for _, l := range srcIdx.Languages() {
				if l = strings.TrimSpace(l); l != "" {
					seen[l] = true
				}
			}
		} else if idx != nil {
			for _, p := range idx.ContentPages() {
				if l := strings.TrimSpace(p.Lang); l != "" {
					seen[l] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for l := range seen {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

func registerGetCapabilities(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config, scopeName string, writeEnabled bool) {
	addReadOnlyTool(s, "get_capabilities", "Get capabilities",
		"Return this server's machine-readable runtime capabilities and hard limits in one structured surface, so an agent can plan deterministically instead of scraping tool descriptions or probing limits by triggering failures (#859): "+
			"`server` (release version, commit, build channel, schema version); `languages` (default, `mode` = `configured` when the operator has set configured_languages and `observed` otherwise, plus the authoritative/observed set accordingly, #899); "+
			"`limits` (body_max_bytes, title_max_runes, asset_max_bytes, test_content_max_ttl_hours, preview_ttl min/default/max seconds, per-caller mutation rate limits); "+
			"`allowed_image_formats` for upload_page_asset; `blocked_shortcodes` the write tools reject; and `features` — coarse availability flags for optional integrations (overall image generation plus its local/external sub-modes, post-build hooks, OAuth, Cloudflare purge, IndexNow, Google indexing, git baseline). "+
			"`features` reports only booleans/counts, never secrets, hook command strings, or host paths. No additional business scope is required beyond the read/anonymous-tier permission; on OAuth-enabled deployments, a Bearer token is still required for every `/mcp` call, including this tool. "+
			"`effective_scopes` names the caller's own scope (`read`, `write`, or `admin` — the three canonical scopes the server ever grants, #450/#1039/#1050) so an agent can tell what it's authorized for without probing; `masked_tools` (omitted entirely when nothing is masked) reports the count of tools NOT visible to this caller with `reason` — `missing_scope` when a read-scoped caller lacks the write tools (or a write-scoped caller lacks the admin-only managed Hugo binary lifecycle tools), or `not_configured` when a write-scoped caller has write tools available but the deployment has no content_root so they never registered — and `required_scopes` naming what would unlock them. `disabled_features[]` separately explains optional integrations disabled by configuration with reason `feature_disabled`; it never exposes secrets or paths. Note: `admin` is intentionally never advertised on the external OAuth-discovery surface (server card, `.well-known/oauth-protected-resource`) — only `read`/`write` appear there, since `admin` is an explicitly-approved administrator tier, not self-service; `effective_scopes` here is this tool's own in-session view of the caller's actual scope, not a mirror of that external discovery contract.",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ getCapabilitiesInput) (*mcp.CallToolResult, getCapabilitiesOutput, error) {
			pMin, pDef, pMax := adminpkg.PreviewTTLBoundsSeconds()
			localHeroGenerationAvailable := strings.TrimSpace(cfg.HugoRoot) != ""
			externalImageGenerationAvailable := strings.TrimSpace(cfg.ImageGenURL) != ""
			languageMode := "observed"
			if len(cfg.ConfiguredLanguages) > 0 {
				languageMode = "configured"
			}
			data := getCapabilitiesData{
				EffectiveScopes:  effectiveCapabilityScopes(ctx, scopeName),
				MaskedTools:      maskedCapabilityTools(ctx, scopeName, writeEnabled),
				DisabledFeatures: disabledCapabilityFeatures(cfg),
				Server: capabilitiesServer{
					ReleaseVersion: buildinfo.Version,
					Commit:         buildinfo.Commit,
					BuildChannel:   buildinfo.EffectiveBuildChannel(),
					SchemaVersion:  buildinfo.SchemaVersion,
				},
				Languages: capabilitiesLanguages{
					Default:   strings.TrimSpace(cfg.DefaultLanguage),
					Mode:      languageMode,
					Available: availableLanguages(idx, srcIdx, cfg.DefaultLanguage, cfg.ConfiguredLanguages),
				},
				Limits: capabilitiesLimits{
					BodyMaxBytes:           writepkg.BodyMaxBytes(),
					TitleMaxRunes:          writepkg.TitleMaxRunes(),
					AssetMaxBytes:          writepkg.AssetMaxBytes(),
					MaxRequestBytes:        cfg.MaxRequestBytes,
					MaxResultItems:         cfg.MaxResultItems,
					TestContentMaxTTLHours: writepkg.TestContentMaxTTLHours(),
					PreviewTTL:             capabilitiesPreviewTTL{MinSeconds: pMin, DefaultSeconds: pDef, MaxSeconds: pMax},
					RateLimits: capabilitiesRateLimits{
						CreateUpdateUploadPerMin: cfg.RateLimit.CreateUpdatePerMin,
						DestructivePerMin:        cfg.RateLimit.DestructivePerMin,
					},
				},
				AllowedImageFormats: writepkg.AllowedAssetExtensions(),
				BlockedShortcodes:   append([]string(nil), cfg.BlockedShortcodes...),
				Features: capabilitiesFeatures{
					ImageGenerationAvailable:         localHeroGenerationAvailable || externalImageGenerationAvailable,
					LocalHeroGenerationAvailable:     localHeroGenerationAvailable,
					ExternalImageGenerationAvailable: externalImageGenerationAvailable,
					PostBuildHooksConfigured:         len(cfg.PostBuildHooks) > 0,
					PostBuildHooksCount:              len(cfg.PostBuildHooks),
					StreamingEnabled:                 cfg.StreamingEnabled,
					OAuthEnabled:                     cfg.OAuth.Enabled,
					ForceDryRunAll:                   cfg.ForceDryRunAll,
					CloudflarePurgeConfigured:        cfg.Cloudflare.Enabled(),
					IndexNowConfigured:               cfg.IndexNow.Enabled(),
					GoogleIndexingConfigured:         cfg.GoogleIndex.Enabled(),
					GitBaselineMode:                  strings.TrimSpace(cfg.GitBaseline.Mode),
				},
			}
			if data.BlockedShortcodes == nil {
				data.BlockedShortcodes = []string{}
			}
			return nil, newGetCapabilitiesOutput(data), nil
		})
}

func disabledCapabilityFeatures(cfg config.Config) []capabilitiesDisabledFeature {
	features := make([]capabilitiesDisabledFeature, 0, 6)
	if strings.TrimSpace(cfg.DBPath) == "" {
		features = append(features, capabilitiesDisabledFeature{Name: "durable_persistence", Reason: "feature_disabled", RequiredConfiguration: "db_path"})
	}
	if !cfg.HugoUpgrade.Enabled {
		features = append(features, capabilitiesDisabledFeature{Name: "hugo_upgrade", Reason: "feature_disabled", RequiredConfiguration: "hugo_upgrade.enabled"})
	}
	if strings.TrimSpace(cfg.ImageGenURL) == "" {
		features = append(features, capabilitiesDisabledFeature{Name: "external_image_generation", Reason: "feature_disabled", RequiredConfiguration: "image_gen_url"})
	}
	if !cfg.Cloudflare.Enabled() {
		features = append(features, capabilitiesDisabledFeature{Name: "cloudflare_purge", Reason: "feature_disabled", RequiredConfiguration: "cloudflare.zone_id, cloudflare.api_token"})
	}
	if !cfg.IndexNow.Enabled() {
		features = append(features, capabilitiesDisabledFeature{Name: "indexnow", Reason: "feature_disabled", RequiredConfiguration: "indexnow.key"})
	}
	if !cfg.GoogleIndex.Enabled() {
		features = append(features, capabilitiesDisabledFeature{Name: "google_indexing", Reason: "feature_disabled", RequiredConfiguration: "google_indexing.service_account_path"})
	}
	return features
}

// fallbackEffectiveScope reports the caller's effective scope when no
// OAuth-verified scope is present in context — OAuth disabled, or a stdio
// session, neither of which ever populates oauth.CtxScope. In both cases
// there is no further per-call scope check, so the physical server tier the
// caller reached (scopeName) is itself their effective access: the "admin"
// tier (stdio, a trusted local transport, see server.NewStdio) and "write"
// tier register write tools unconditionally, and the public/"" tier
// registers read tools unconditionally (see newScopedServer), so reporting
// "anonymous" or "read" there would be false.
func fallbackEffectiveScope(scopeName string) string {
	switch scopeName {
	case "admin":
		return "admin"
	case "write":
		return "write"
	default:
		return "read"
	}
}

func effectiveCapabilityScopes(ctx context.Context, scopeName string) []string {
	if scope, _ := ctx.Value(oauth.CtxScope).(string); strings.TrimSpace(scope) != "" {
		return []string{strings.TrimSpace(scope)}
	}
	return []string{fallbackEffectiveScope(scopeName)}
}

// toolCountForScope is the number of tools whose RequiredScope is exactly
// scope, across every package a scoped server can register (write, admin —
// read's own tools never require a scope beyond read/anonymous, see Defs()).
func toolCountForScope(scope string) int {
	count := 0
	for _, def := range append(writepkg.Defs(), adminpkg.Defs()...) {
		if def.RequiredScope == scope {
			count++
		}
	}
	return count
}

// maskedCapabilityTools reports the higher-tier tools this caller cannot
// see, or nil when nothing is masked. effective is always exactly "read",
// "write", or "admin": every OAuth-verified scope is canonicalized to one of
// those three (oauth.CanonicalScope), and fallbackEffectiveScope never
// produces anything else either.
func maskedCapabilityTools(ctx context.Context, scopeName string, writeEnabled bool) *capabilitiesMaskedTools {
	scope, _ := ctx.Value(oauth.CtxScope).(string)
	effective := strings.TrimSpace(scope)
	if effective == "" {
		effective = fallbackEffectiveScope(scopeName)
	}
	switch effective {
	case "read":
		// Both write- and admin-gated tools are invisible to a read-only
		// caller; "write" is the immediate scope that would unlock write
		// tools (admin is a further step beyond that), so it's reported as
		// the single required scope, but the count includes everything a
		// read caller cannot currently see.
		return &capabilitiesMaskedTools{Count: toolCountForScope("write") + toolCountForScope("admin"), Reason: "missing_scope", Scopes: []string{"write"}}
	case "admin":
		// admin implies write, so nothing is scope-gated beyond this — but
		// write (and therefore admin) tools only actually register when the
		// deployment has a content_root configured (see newScopedServer's
		// `(scopeName == "write" || scopeName == "admin") && writeEnabled`
		// guard). Without it, an admin-scoped caller still can't see them.
		if !writeEnabled {
			return &capabilitiesMaskedTools{Count: toolCountForScope("write") + toolCountForScope("admin"), Reason: "not_configured", Scopes: []string{"write"}}
		}
		return nil
	default: // effective == "write"
		// write-tier scope alone grants access to write tools, but not to
		// the admin-gated managed Hugo binary lifecycle tools.
		if !writeEnabled {
			return &capabilitiesMaskedTools{Count: toolCountForScope("write") + toolCountForScope("admin"), Reason: "not_configured", Scopes: []string{"write"}}
		}
		return &capabilitiesMaskedTools{Count: toolCountForScope("admin"), Reason: "missing_scope", Scopes: []string{"admin"}}
	}
}
