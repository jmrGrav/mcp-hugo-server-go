package config

import (
	"os"
	"strings"
)

// applyEnvOverlay fills a handful of identity/deployment fields from
// MCP_HUGO_*-namespaced environment variables when the field is still empty
// (its zero value) after loading from file (or Default(), if no file was
// given at all).
//
// This exists for the MCPB/stdio distribution channel (#782 Phase 2): an
// MCPB install has no config.yaml — the manifest's ${user_config.*}
// substitution can only inject values into the launched process's env or
// args, it cannot write a file — so site_root/hugo_root/content_root/
// site_url/site_name need an env-var path into Config that doesn't require
// one.
//
// Precedence is deliberately file-wins, env-fills-gaps, applied identically
// whether or not a file was loaded: a field is only set from its env var if
// it's still empty. This is the one guarantee that matters for every
// existing HTTP deployment (which always sets these fields explicitly in
// config.yaml): an ambient MCP_HUGO_* variable can never override a value
// the operator's own file already set. TestApplyEnvOverlayDoesNotChangeConfigWithFileAndNoEnv
// is the regression test for exactly this — a config file plus an empty
// environment must produce a byte-for-byte identical Config to before this
// overlay existed.
func applyEnvOverlay(cfg *Config) {
	fillFromEnv(&cfg.SiteRoot, "MCP_HUGO_SITE_ROOT")
	fillFromEnv(&cfg.HugoRoot, "MCP_HUGO_HUGO_ROOT")
	fillFromEnv(&cfg.ContentRoot, "MCP_HUGO_CONTENT_ROOT")
	fillFromEnv(&cfg.SiteURL, "MCP_HUGO_SITE_URL")
	fillFromEnv(&cfg.SiteName, "MCP_HUGO_SITE_NAME")
}

func fillFromEnv(field *string, envVar string) {
	if strings.TrimSpace(*field) != "" {
		return
	}
	if v := strings.TrimSpace(os.Getenv(envVar)); v != "" {
		*field = v
	}
}
