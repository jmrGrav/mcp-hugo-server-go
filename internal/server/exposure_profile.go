package server

// Exposure profiles (#1137) are a pure discoverability filter, not a
// security boundary — deliberately distinct from site.AccessProfile*
// (data-scoping inside tool responses) and from OAuth scope (the actual
// authorization control, enforced by oauth.ScopePolicy regardless of
// which profile a caller selects). A caller's OAuth scope still governs
// exactly which tools they are ALLOWED to call; an exposure profile only
// narrows which of those allowed tools are actually registered on the
// *mcp.Server instance their session connects to.
//
// That narrowing is enforced at the transport level via mcp.Server's own
// RemoveTools, which deletes the tool from the same map both tools/list
// and CallTool dispatch read from (see the go-sdk's featureSet.remove and
// Server.callTool) — a tool hidden by a profile is not merely undiscovered,
// it genuinely cannot be called through that connection. A caller who
// wants a wider tool set reconnects with a different (or no) `profile`
// query parameter — no new OAuth grant or token exchange required, since
// the underlying scope is unchanged. This is intentionally more than
// "just a filter" in the strict sense the issue's own framing suggests;
// document it this way rather than repeating that framing uncorrected.
const (
	ExposureProfileReader    = "reader"
	ExposureProfileEditorial = "editorial"
	ExposureProfileAdvanced  = "advanced"
	ExposureProfileAdmin     = "admin"
)

// exposureProfileQueryParam is the connection-time opt-in (#1137): absent
// entirely by default, so every existing integration's tool list is
// unchanged unless it explicitly starts passing this parameter. The issue
// also offered an OAuth-client-registration-time alternative; that path
// needs a token-store/client-registry schema change and is deliberately
// deferred — the query parameter needs neither and covers the same need.
const exposureProfileQueryParam = "profile"

// toolExposureTier maps every registered tool name to the minimum
// exposure profile that makes it visible. Profiles are cumulative ranks
// (see exposureProfileRank): a tool tiered "reader" is visible under
// reader/editorial/advanced/admin; a tool tiered "admin" is visible only
// under the admin profile.
//
// reader (~15, curated per the issue's own examples): read-only content
// discovery and basic health/link checks — the smallest surface a
// content-reading agent needs, with no ability to mutate anything and no
// operational/publishing detail.
//
// editorial (+~14): the day-to-day content workflow on top of reader —
// create/update/delete a page and its assets, preview before publishing,
// check for problems, and actually publish. Matches the issue's own
// "+ create_page, update_page, upload_page_asset, create_preview" example
// plus the natural companions a real editorial workflow needs (delete
// pairs with create, diff/validate happen before publish, build_site/
// publish_changes/get_mutation_status close the loop).
//
// admin (4): the managed-Hugo-binary lifecycle tools already excluded
// from the write scope's own tools/list at buildWriteScopedServer — see
// its RemoveTools call. Exposure-profile-admin is the only profile that
// re-admits them (still subject to the caller's own OAuth scope actually
// being "admin"; the profile never widens what scope allows).
//
// advanced: every other registered tool — bundle/SRI/build/hooks/rollback
// operations, and the remaining read-side tools not curated into reader
// (agent-context export, revision history, structural/orphan-link
// analysis) that a casual content agent rarely needs but an automation
// pipeline does. Assigning these "advanced" by being the catch-all
// (rather than hand-listing all ~36) keeps this table's exceptions
// visible instead of burying them in a long flat list; the completeness
// invariant is enforced by TestToolExposureTierCoversEveryRegisteredTool.
var toolExposureTier = map[string]string{
	// reader
	"get_page":             ExposureProfileReader,
	"list_pages":           ExposureProfileReader,
	"search_pages":         ExposureProfileReader,
	"search_content":       ExposureProfileReader,
	"get_recent_posts":     ExposureProfileReader,
	"list_tags":            ExposureProfileReader,
	"list_categories":      ExposureProfileReader,
	"get_sitemap":          ExposureProfileReader,
	"get_feed":             ExposureProfileReader,
	"get_site_information": ExposureProfileReader,
	"get_related_content":  ExposureProfileReader,
	"list_content_types":   ExposureProfileReader,
	"get_broken_links":     ExposureProfileReader,
	"inspect_rendered":     ExposureProfileReader,
	"get_site_health":      ExposureProfileReader,

	// editorial
	"create_page":          ExposureProfileEditorial,
	"update_page":          ExposureProfileEditorial,
	"delete_page":          ExposureProfileEditorial,
	"upload_page_asset":    ExposureProfileEditorial,
	"delete_page_asset":    ExposureProfileEditorial,
	"create_preview":       ExposureProfileEditorial,
	"list_previews":        ExposureProfileEditorial,
	"revoke_preview":       ExposureProfileEditorial,
	"inspect_preview":      ExposureProfileEditorial,
	"diff_page":            ExposureProfileEditorial,
	"validate_frontmatter": ExposureProfileEditorial,
	"get_mutation_status":  ExposureProfileEditorial,
	"publish_changes":      ExposureProfileEditorial,
	"build_site":           ExposureProfileEditorial,

	// admin (managed Hugo binary lifecycle — matches buildWriteScopedServer's
	// own RemoveTools list exactly; keep both in sync if either changes)
	"stage_hugo_upgrade": ExposureProfileAdmin,
	"activate_hugo":      ExposureProfileAdmin,
	"rollback_hugo":      ExposureProfileAdmin,
	"bootstrap_hugo":     ExposureProfileAdmin,

	// advanced (everything else)
	"apply_bundle_plan":    ExposureProfileAdvanced,
	"apply_content_plan":   ExposureProfileAdvanced,
	"build_agent_context":  ExposureProfileAdvanced,
	"check_ai_readiness":   ExposureProfileAdvanced,
	"check_sri_versions":   ExposureProfileAdvanced,
	"create_bundle":        ExposureProfileAdvanced,
	"create_change_set":    ExposureProfileAdvanced,
	"delete_bundle":        ExposureProfileAdvanced,
	"explain_structure":    ExposureProfileAdvanced,
	"export_agent_context": ExposureProfileAdvanced,
	"generate_hero_image":  ExposureProfileAdvanced,
	"get_backlinks":        ExposureProfileAdvanced,
	"get_capabilities":     ExposureProfileAdvanced,
	"get_changelog":        ExposureProfileAdvanced,
	"get_hugo_update":      ExposureProfileAdvanced,
	"get_page_for_edit":    ExposureProfileAdvanced,
	"get_page_frontmatter": ExposureProfileAdvanced,
	"get_page_markdown":    ExposureProfileAdvanced,
	"get_rate_limits":      ExposureProfileAdvanced,
	"get_runtime_status":   ExposureProfileAdvanced,
	"get_storage_health":   ExposureProfileAdvanced,
	"get_theme_status":     ExposureProfileAdvanced,
	"list_page_assets":     ExposureProfileAdvanced,
	"list_page_revisions":  ExposureProfileAdvanced,
	"list_page_snapshots":  ExposureProfileAdvanced,
	"plan_bundle_change":   ExposureProfileAdvanced,
	"plan_content_change":  ExposureProfileAdvanced,
	"plan_page":            ExposureProfileAdvanced,
	"preview_build":        ExposureProfileAdvanced,
	"revoke_all_previews":  ExposureProfileAdvanced,
	"rollback_bundle":      ExposureProfileAdvanced,
	"rollback_change":      ExposureProfileAdvanced,
	"run_post_build_hooks": ExposureProfileAdvanced,
	"suggest_links":        ExposureProfileAdvanced,
	"validate_site":        ExposureProfileAdvanced,
	"verify_publication":   ExposureProfileAdvanced,
}

// exposureProfileRank returns the integer rank of a known exposure
// profile (reader < editorial < advanced < admin), or -1 for an unknown
// value. Unlike tools.ScopeRank, an unknown profile is NOT silently
// treated as the lowest rank — see isKnownExposureProfile, which callers
// must use to reject a typo'd profile outright rather than silently
// falling through to an unfiltered (or over-filtered) tool list.
func exposureProfileRank(profile string) int {
	switch profile {
	case ExposureProfileReader:
		return 0
	case ExposureProfileEditorial:
		return 1
	case ExposureProfileAdvanced:
		return 2
	case ExposureProfileAdmin:
		return 3
	default:
		return -1
	}
}

// isKnownExposureProfile reports whether profile is one of the four
// recognized values. Called before any filtering happens so a malformed
// ?profile= query parameter is rejected outright rather than silently
// yielding an unfiltered (surprising-widen) or fully-empty
// (surprising-narrow) tool list — a typo'd profile must fail loudly.
func isKnownExposureProfile(profile string) bool {
	return exposureProfileRank(profile) >= 0
}

// toolsToHideForExposureProfile returns the subset of allToolNames whose
// tier rank exceeds profile's rank — the exact set to pass to
// (*mcp.Server).RemoveTools to narrow a fully-registered scoped server
// down to profile's visible set. A tool with no tier entry is treated as
// "advanced" (rank 2), the same default a newly added tool gets before
// someone deliberately curates it into reader/editorial — never silently
// dropped from every profile, never silently admitted into reader.
// scopeNameForRank maps an OAuth scope rank back to the scopeName strings
// newScopedServer/buildWriteScopedServer/buildAdminScopedServer key on
// ("", "write", "admin") — the same three-way split the unfiltered
// publicServer/writeServer/adminServer selection already uses.
func scopeNameForRank(rank int) string {
	switch {
	case rank >= 2:
		return "admin"
	case rank >= 1:
		return "write"
	default:
		return ""
	}
}

// exposureServerKey identifies one (scope, profile) cell in New()'s
// exposureServers matrix.
func exposureServerKey(scopeName, profile string) string {
	return scopeName + "|" + profile
}

func toolsToHideForExposureProfile(allToolNames []string, profile string) []string {
	profileRank := exposureProfileRank(profile)
	var hide []string
	for _, name := range allToolNames {
		tier, ok := toolExposureTier[name]
		if !ok {
			tier = ExposureProfileAdvanced
		}
		if exposureProfileRank(tier) > profileRank {
			hide = append(hide, name)
		}
	}
	return hide
}
