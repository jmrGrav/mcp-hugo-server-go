package anonymous

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/oauth"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolcontract"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/toolregistry"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/tools"
	adminpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/admin"
	readpkg "github.com/jmrGrav/mcp-hugo-server-go/internal/tools/read"
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
	BodyMaxBytes           int                     `json:"body_max_bytes"`
	TitleMaxRunes          int                     `json:"title_max_runes"`
	AssetMaxBytes          int                     `json:"asset_max_bytes"`
	MaxRequestBytes        int64                   `json:"max_request_bytes,omitempty"`
	MaxResultItems         int                     `json:"max_result_items,omitempty"`
	TestContentMaxTTLHours int                     `json:"test_content_max_ttl_hours"`
	PreviewTTL             capabilitiesPreviewTTL  `json:"preview_ttl"`
	RateLimits             capabilitiesRateLimits  `json:"rate_limits"`
	AssetUpload            capabilitiesAssetUpload `json:"asset_upload"`
}

// capabilitiesAssetUpload (#1190) reconciles asset_max_bytes and
// max_request_bytes into the one number that actually matters for
// upload_page_asset's only input mode today (content_base64 inline in the
// tool-call JSON): the largest decoded file an agent can upload in a single
// call. asset_max_bytes alone overstates what's reachable — base64 expands
// the payload ~4/3, and it still has to fit inside max_request_bytes
// alongside the rest of the tool-call envelope (slug, filename, other
// params). recommended_inline_max_bytes is computed from the server's
// actual configured max_request_bytes (not a hardcoded constant), so it
// stays correct on any deployment that raised or lowered that limit.
// There is deliberately no chunked-upload field yet — upload_page_asset has
// no chunked mode; advertising one that doesn't exist would be worse than
// the ambiguity this field fixes.
type capabilitiesAssetUpload struct {
	MaxAssetBytes             int `json:"max_asset_bytes"`
	RecommendedInlineMaxBytes int `json:"recommended_inline_max_bytes"`
}

// requestEnvelopeOverheadBytes is a conservative reservation for everything
// in an upload_page_asset tool-call JSON body besides content_base64 itself
// (the JSON-RPC envelope, tool name, slug/filename/content_type params) —
// deliberately generous so recommended_inline_max_bytes stays a safe
// under-estimate rather than a value that can still hit max_request_bytes.
const requestEnvelopeOverheadBytes = 2048

// recommendedInlineAssetMaxBytes returns the largest decoded asset size an
// agent can reliably send through upload_page_asset's inline content_base64
// param in one call, given this server's actual configured
// max_request_bytes — never more than assetMaxBytes itself.
func recommendedInlineAssetMaxBytes(maxRequestBytes int64, assetMaxBytes int) int {
	if maxRequestBytes <= 0 {
		maxRequestBytes = 1 << 20
	}
	available := maxRequestBytes - requestEnvelopeOverheadBytes
	if available <= 0 {
		return 0
	}
	decoded := available * 3 / 4 // base64 encoding expands by 4/3
	if decoded > int64(assetMaxBytes) {
		decoded = int64(assetMaxBytes)
	}
	if decoded < 0 {
		decoded = 0
	}
	return int(decoded)
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
	ToolCatalog         capabilitiesToolCatalog       `json:"tool_catalog"`
	AllowedImageFormats []string                      `json:"allowed_image_formats"`
	BlockedShortcodes   []string                      `json:"blocked_shortcodes"`
	Features            capabilitiesFeatures          `json:"features"`
}

// capabilitiesToolCatalog (#1175) lets an agent detect a stale client-side
// MCP tool-schema cache: compare visible_count/tool_names_revision against
// what its own connector actually loaded (its tools/list result). A
// mismatch means the client is looking at a stale discovery snapshot, not
// that the server is misconfigured — the concrete case #1175 was filed
// over: two MCP clients against the same server commit disagreed on the
// registered tool set, and neither had a way to detect that on its own.
//
// Scope-only, like masked_tools: this does NOT account for exposure-profile
// session narrowing (?profile=, #1137) — a profiled session's actual
// tools/list can be a subset of visible_count. Same documented gap
// masked_tools already has (see
// TestExposureProfileAdminScopeMaskedToolsDoesNotCountProfileHidden).
//
// tool_names_revision hashes sorted tool NAMES only, not descriptions or
// input schemas — it moves when a tool is added or removed, but NOT when
// an existing tool's schema gains/loses a field (e.g. #1175's own
// include_responsive_summary example). Named for exactly what it covers
// rather than "catalog_revision" or similar, which would overpromise
// schema-level staleness detection this doesn't do.
//
// RegistryDigest (#1225) does NOT share tool_names_revision's
// exposure-profile blind spot: it is computed from THIS session's own
// *mcp.Server (see toolRegistryDigestCache), the same object
// ?profile=/RemoveTools (#1137) narrows before any session ever reaches
// it, so a tool hidden from this session is genuinely absent from the
// digest, and RemoveTools-ing a profile genuinely moves it.
type capabilitiesToolCatalog struct {
	VisibleCount   int    `json:"visible_count"`
	NamesRevision  string `json:"tool_names_revision"`
	RegistryDigest string `json:"tool_registry_digest,omitempty"`
}

// toolRegistryDigestCache computes and caches the #1225 tool-registry
// digest for one specific *mcp.Server instance, via a real in-memory
// tools/list round-trip against s (internal/toolregistry.FromServer) —
// not a second internal registration list that could silently diverge
// from what a real client sees.
//
// Computing this INSIDE a running tool handler (rather than once at
// startup against a separately-built admin superset, this field's
// original #1225 design) is what makes it exposure-profile-correct:
// exposure-profile narrowing (buildExposureServer's RemoveTools,
// internal/server/server.go, #1137) and write-scope's own
// RemoveTools("stage_hugo_upgrade", ...) both mutate this exact *mcp.Server
// object before any session ever reaches a handler on it, so introspecting
// s here sees precisely what THIS session's own tools/list already
// reflects — the same guarantee tool_names_revision above does not have.
//
// Reentrancy: s.Connect (inside FromServer) is called from within a
// handler already executing on a session of s. This is safe because the
// go-sdk's Server.callTool looks up and releases the tool registry lock
// before invoking the handler (github.com/modelcontextprotocol/go-sdk
// mcp/server.go's callTool: s.getServerTool locks/unlocks, then calls
// st.handler outside the lock) — no lock held across handler execution
// for Connect to contend with.
// TestGetCapabilitiesDigestConcurrentCallsDoNotDeadlock exercises this
// under -race with two live sessions calling get_capabilities
// concurrently.
//
// A cache instance is owned by exactly one registerGetCapabilities
// closure (one per constructed server), so caching here is safe: by the
// time any handler actually runs, that server's full construction —
// including any later RemoveTools — has already completed
// (registerGetCapabilities itself only ever runs during construction,
// never concurrently with a live request).
//
// Only a SUCCESSFUL computation is latched. A transient failure (e.g. the
// calling client disconnects mid-request on this session's very first
// get_capabilities call, canceling ctx before the in-memory round-trip
// finishes) must not permanently disable the field for this server's
// entire remaining lifetime — the next caller gets a fresh attempt, not a
// silently-and-forever-omitted field with no way to tell "this
// deployment doesn't support the digest" from "one early call happened
// to fail."
type toolRegistryDigestCache struct {
	mu     sync.Mutex
	digest string
}

func (c *toolRegistryDigestCache) get(ctx context.Context, s *mcp.Server) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.digest != "" {
		return c.digest
	}
	snapshots, err := toolregistry.FromServer(ctx, s)
	if err != nil {
		slog.Warn("get_capabilities: tool registry digest snapshot failed", "error", err)
		return ""
	}
	digest, err := toolregistry.Digest(snapshots)
	if err != nil {
		slog.Warn("get_capabilities: tool registry digest computation failed", "error", err)
		return ""
	}
	c.digest = digest
	return c.digest
}

// visibleToolCatalog computes capabilitiesToolCatalog from the same
// tools.Registry.ForScope machinery the access-model contract tests
// verify against actual per-scope tool registration — the authoritative
// source for "which tool names does this scope see." registryDigest is
// computed separately (toolRegistryDigestCache) since it needs the
// real *mcp.Server, which this scope/writeEnabled-only reconstruction
// deliberately avoids depending on.
func visibleToolCatalog(scopeName string, writeEnabled bool, registryDigest string) capabilitiesToolCatalog {
	reg := tools.NewRegistry()
	for _, d := range Defs() {
		reg.Register(d)
	}
	for _, d := range readpkg.Defs() {
		reg.Register(d)
	}
	if writeEnabled {
		for _, d := range writepkg.Defs() {
			reg.Register(d)
		}
		for _, d := range adminpkg.Defs() {
			reg.Register(d)
		}
	}

	defs := reg.ForScope(fallbackEffectiveScope(scopeName))
	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, n := range names {
		h.Write([]byte(n))
		h.Write([]byte{0})
	}
	return capabilitiesToolCatalog{
		VisibleCount:   len(names),
		NamesRevision:  "sha256:" + hex.EncodeToString(h.Sum(nil)),
		RegistryDigest: registryDigest,
	}
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
	var digestCache toolRegistryDigestCache
	addReadOnlyTool(s, "get_capabilities", "Get capabilities",
		"Return this server's machine-readable runtime capabilities and hard limits in one structured surface, so an agent can plan deterministically instead of scraping tool descriptions or probing limits by triggering failures (#859): "+
			"`server` (release version, commit, build channel, schema version); `languages` (default, `mode` = `configured` when the operator has set configured_languages and `observed` otherwise, plus the authoritative/observed set accordingly, #899); "+
			"`limits` (body_max_bytes, title_max_runes, asset_max_bytes, test_content_max_ttl_hours, preview_ttl min/default/max seconds, per-caller mutation rate limits, `asset_upload`); "+
			"`limits.asset_upload.recommended_inline_max_bytes` (#1190) is the actually-reachable ceiling for upload_page_asset's inline `content_base64` param — computed from this server's real `max_request_bytes` (base64 expands the payload ~4/3, and it still has to fit the tool-call envelope alongside `max_request_bytes`), which is always <= `asset_max_bytes` and often well below it; a file larger than this needs a different transfer path (there is no chunked-upload mode yet) before uploading in one call. "+
			"`allowed_image_formats` for upload_page_asset; `blocked_shortcodes` the write tools reject; and `features` — coarse availability flags for optional integrations (overall image generation plus its local/external sub-modes, post-build hooks, OAuth, Cloudflare purge, IndexNow, Google indexing, git baseline). "+
			"`features` reports only booleans/counts, never secrets, hook command strings, or host paths. No additional business scope is required beyond the read/anonymous-tier permission; on OAuth-enabled deployments, a Bearer token is still required for every `/mcp` call, including this tool. "+
			"`effective_scopes` names the caller's own scope (`read`, `write`, or `admin` — the three canonical scopes the server ever grants, #450/#1039/#1050) so an agent can tell what it's authorized for without probing; `masked_tools` (omitted entirely when nothing is masked) reports the count of tools NOT visible to this caller with `reason` — `missing_scope` when a read-scoped caller lacks the write tools (or a write-scoped caller lacks the admin-only managed Hugo binary lifecycle tools), or `not_configured` when a write-scoped caller has write tools available but the deployment has no content_root so they never registered — and `required_scopes` naming what would unlock them. `disabled_features[]` separately explains optional integrations disabled by configuration with reason `feature_disabled`; it never exposes secrets or paths. `tool_catalog` (#1175) is a stale-discovery detector: `visible_count`/`tool_names_revision` (a sha256 over this caller's scope-visible tool names, sorted) let an agent compare what its own MCP client actually loaded against what this server currently registers for it — a mismatch means the client's tool-schema cache is stale, not that the server is misconfigured (the concrete case #1175 was filed over: two MCP clients against the same server commit disagreed on the registered tool set). Scope-only like `masked_tools` — does not account for exposure-profile session narrowing (`?profile=`, #1137). `tool_names_revision` hashes tool names only, not descriptions/input schemas, so it does not move when an existing tool's schema alone changes (only when a tool is added or removed) — read it as a name-catalog fingerprint, not a full-schema one. `tool_registry_digest` (#1225, omitted when unavailable) closes that gap for tool-poisoning / rug-pull detection: a sha256 over name+description+input schema+output schema across every tool THIS SESSION can see — computed from the same tools/list surface exposure profiles (`?profile=`, #1137) and scope already narrowed, unlike `tool_names_revision`, so a profile-hidden tool never moves it and a profile change always does. A client should pin the digest observed from a specific, already-trusted deployment+scope+profile combination and compare on reconnect to that same combination — a mismatch means that connection's published tool set changed (a version upgrade, a config/profile change, or a tampered/compromised server), not that something is universally wrong; a mismatch against a value observed under a DIFFERENT scope or profile is expected and not evidence of tampering. See docs/mcp-contract.md §6.28. Note: `admin` is intentionally never advertised on the external OAuth-discovery surface (server card, `.well-known/oauth-protected-resource`) — only `read`/`write` appear there, since `admin` is an explicitly-approved administrator tier, not self-service; `effective_scopes` here is this tool's own in-session view of the caller's actual scope, not a mirror of that external discovery contract.",
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
				ToolCatalog:      visibleToolCatalog(scopeName, writeEnabled, digestCache.get(ctx, s)),
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
					AssetUpload: capabilitiesAssetUpload{
						MaxAssetBytes:             writepkg.AssetMaxBytes(),
						RecommendedInlineMaxBytes: recommendedInlineAssetMaxBytes(cfg.MaxRequestBytes, writepkg.AssetMaxBytes()),
					},
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
