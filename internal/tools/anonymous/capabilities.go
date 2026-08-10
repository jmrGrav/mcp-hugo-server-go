package anonymous

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
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
	Server              capabilitiesServer    `json:"server"`
	Languages           capabilitiesLanguages `json:"languages"`
	Limits              capabilitiesLimits    `json:"limits"`
	AllowedImageFormats []string              `json:"allowed_image_formats"`
	BlockedShortcodes   []string              `json:"blocked_shortcodes"`
	Features            capabilitiesFeatures  `json:"features"`
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

func registerGetCapabilities(s *mcp.Server, idx *site.Index, srcIdx *hugosite.SourceIndex, cfg config.Config) {
	addReadOnlyTool(s, "get_capabilities", "Get capabilities",
		"Return this server's machine-readable runtime capabilities and hard limits in one structured surface, so an agent can plan deterministically instead of scraping tool descriptions or probing limits by triggering failures (#859): "+
			"`server` (release version, commit, build channel, schema version); `languages` (default, `mode` = `configured` when the operator has set configured_languages and `observed` otherwise, plus the authoritative/observed set accordingly, #899); "+
			"`limits` (body_max_bytes, title_max_runes, asset_max_bytes, test_content_max_ttl_hours, preview_ttl min/default/max seconds, per-caller mutation rate limits); "+
			"`allowed_image_formats` for upload_page_asset; `blocked_shortcodes` the write tools reject; and `features` — coarse availability flags for optional integrations (overall image generation plus its local/external sub-modes, post-build hooks, OAuth, Cloudflare purge, IndexNow, Google indexing, git baseline). "+
			"`features` reports only booleans/counts, never secrets, hook command strings, or host paths. No additional business scope is required beyond the read/anonymous-tier permission; on OAuth-enabled deployments, a Bearer token is still required for every `/mcp` call, including this tool.",
		func(_ context.Context, _ *mcp.CallToolRequest, _ getCapabilitiesInput) (*mcp.CallToolResult, getCapabilitiesOutput, error) {
			pMin, pDef, pMax := adminpkg.PreviewTTLBoundsSeconds()
			localHeroGenerationAvailable := strings.TrimSpace(cfg.HugoRoot) != ""
			externalImageGenerationAvailable := strings.TrimSpace(cfg.ImageGenURL) != ""
			languageMode := "observed"
			if len(cfg.ConfiguredLanguages) > 0 {
				languageMode = "configured"
			}
			data := getCapabilitiesData{
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
