package read_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/hugosite"
)

func TestValidateAIReadinessPassesForWellStructuredPage(t *testing.T) {
	contentRoot := t.TempDir()
	writePage(t, contentRoot, "posts/ready/index.md", `---
title: Ready
date: 2026-07-19
summary: Structured enough for agent workflows.
tags: [mcp]
categories: [docs]
---

## Context

This section introduces the page and links back into the site with a [reference](/posts/other/).

## Details

This section stays short and segmented.
`)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()

	session, done := newTestClientWithCfg(t, nil, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "check_ai_readiness", map[string]any{"slug": "/posts/ready/"})
	if res.IsError {
		t.Fatalf("check_ai_readiness returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	if got := data["status"]; got != "pass" {
		t.Fatalf("status = %v, want pass", got)
	}
	if got := data["resolved_source_path"]; got != "content/posts/ready/index.md" {
		t.Fatalf("resolved_source_path = %v, want content/posts/ready/index.md", got)
	}
	checks, ok := data["checks"].(map[string]any)
	if !ok {
		t.Fatalf("checks type = %T, want map", data["checks"])
	}
	if got := checks["heading_hierarchy"].(map[string]any)["status"]; got != "pass" {
		t.Fatalf("heading_hierarchy status = %v, want pass", got)
	}
	if got := checks["metadata_presence"].(map[string]any)["status"]; got != "pass" {
		t.Fatalf("metadata_presence status = %v, want pass", got)
	}
}

func TestValidateAIReadinessReportsDeterministicWarningsAndFailures(t *testing.T) {
	contentRoot := t.TempDir()
	body := `---
categories: [docs]
---

##Broken

` + longRunes('x', 920) + `

` + longRunes('y', 2600) + `
`
	writePage(t, contentRoot, "posts/problem/index.md", body)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()

	session, done := newTestClientWithCfg(t, nil, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "check_ai_readiness", map[string]any{"slug": "/posts/problem/"})
	if res.IsError {
		t.Fatalf("check_ai_readiness returned error: %v", res.Content)
	}
	data := decodeContent(t, res)
	if got := data["status"]; got != "fail" {
		t.Fatalf("status = %v, want fail", got)
	}
	checks := data["checks"].(map[string]any)
	if got := checks["heading_hierarchy"].(map[string]any)["status"]; got != "fail" {
		t.Fatalf("heading_hierarchy status = %v, want fail", got)
	}
	if got := checks["metadata_presence"].(map[string]any)["status"]; got != "fail" {
		t.Fatalf("metadata_presence status = %v, want fail", got)
	}
	if got := checks["paragraph_lengths"].(map[string]any)["status"]; got != "warn" {
		t.Fatalf("paragraph_lengths status = %v, want warn", got)
	}
	if got := checks["section_lengths"].(map[string]any)["status"]; got != "warn" {
		t.Fatalf("section_lengths status = %v, want warn", got)
	}
}

func TestValidateAIReadinessRejectsBlankSlug(t *testing.T) {
	idx := mustTestIndex(t)
	srcIdx := mustTestSourceIndex(t)
	cfg := config.Default()
	cfg.ContentRoot = filepath.Join("..", "..", "..", "testdata", "fixtures", "content")

	session, done := newTestClientWithCfg(t, idx, cfg, srcIdx)
	defer done()

	res := callTool(t, session, "check_ai_readiness", map[string]any{"slug": "   "})
	if !res.IsError {
		t.Fatal("check_ai_readiness(blank slug): want error, got success")
	}
	errEnv := decodeErrorEnvelope(t, res)
	errors, ok := errEnv["errors"].([]any)
	if !ok || len(errors) == 0 {
		t.Fatalf("errors = %#v, want at least one structured error", errEnv["errors"])
	}
	if got := errors[0].(map[string]any)["code"]; got != "missing_required_parameter" {
		t.Fatalf("error code = %v, want missing_required_parameter", got)
	}
}

// TestValidateAIReadinessLangSelectsTranslation is the #866-item-6 / #850
// regression: check_ai_readiness must honour an explicit `lang` and audit the
// requested translation of a bilingual bundle, not implicitly resolve the same
// one regardless. This is the resolution path the cross-language leak #867
// lived in, so the test asserts en and fr resolve to *genuinely different*
// translation files (resolved_lang + resolved_source_path both diverge). It
// fails red against the pre-fix implicit-only resolver.Resolve(slug) call,
// which ignored `lang` and returned one translation for both requests.
func TestValidateAIReadinessLangSelectsTranslation(t *testing.T) {
	contentRoot := t.TempDir()
	writePage(t, contentRoot, "posts/bilingual/index.en.md", `---
title: English Title
date: 2026-07-19
summary: English summary structured for agents.
tags: [mcp]
categories: [docs]
---

## Context

English body with a [reference](/posts/other/).

## Details

English details, kept short.
`)
	writePage(t, contentRoot, "posts/bilingual/index.fr.md", `---
title: Titre Français
date: 2026-07-19
summary: Résumé français structuré pour les agents.
tags: [mcp]
categories: [docs]
---

## Contexte

Corps français avec une [référence](/posts/other/).

## Détails

Détails français, gardés courts.
`)
	srcIdx, err := hugosite.NewSourceIndex(contentRoot)
	if err != nil {
		t.Fatalf("NewSourceIndex() error = %v", err)
	}
	cfg := config.Default()
	cfg.ContentRoot = contentRoot
	cfg.SiteRoot = t.TempDir()

	session, done := newTestClientWithCfg(t, nil, cfg, srcIdx)
	defer done()

	enRes := callTool(t, session, "check_ai_readiness", map[string]any{"slug": "posts/bilingual", "lang": "en"})
	if enRes.IsError {
		t.Fatalf("check_ai_readiness(lang=en) returned error: %v", enRes.Content)
	}
	frRes := callTool(t, session, "check_ai_readiness", map[string]any{"slug": "posts/bilingual", "lang": "fr"})
	if frRes.IsError {
		t.Fatalf("check_ai_readiness(lang=fr) returned error: %v", frRes.Content)
	}
	enData := decodeContent(t, enRes)
	frData := decodeContent(t, frRes)

	if enData["resolved_lang"] != "en" {
		t.Fatalf("en resolved_lang = %v, want en", enData["resolved_lang"])
	}
	if frData["resolved_lang"] != "fr" {
		t.Fatalf("fr resolved_lang = %v, want fr", frData["resolved_lang"])
	}
	if enData["resolved_lang"] == frData["resolved_lang"] {
		t.Fatalf("lang was ignored: en and fr resolved to the same translation %v", enData["resolved_lang"])
	}
	enPath, _ := enData["resolved_source_path"].(string)
	frPath, _ := frData["resolved_source_path"].(string)
	if !strings.HasSuffix(enPath, "index.en.md") {
		t.Fatalf("en resolved_source_path = %q, want suffix index.en.md", enPath)
	}
	if !strings.HasSuffix(frPath, "index.fr.md") {
		t.Fatalf("fr resolved_source_path = %q, want suffix index.fr.md", frPath)
	}
}

func writePage(t *testing.T, contentRoot, relPath, body string) {
	t.Helper()
	full := filepath.Join(contentRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", full, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", full, err)
	}
}

func longRunes(ch rune, n int) string {
	out := make([]rune, n)
	for i := range out {
		out[i] = ch
	}
	return string(out)
}
