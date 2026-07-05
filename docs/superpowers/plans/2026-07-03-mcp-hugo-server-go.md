# mcp-hugo-server-go Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> Historical note: this plan predates the v1.2.10 scope simplification. Current canonical scopes are `content.read`, `content.write`, and `site.admin`; legacy `system.admin` normalizes to `site.admin`.

**Goal:** Build the canonical unified MCP server for the arleo.eu Hugo site, merging the public read-only surface (`hugo-public-mcp`) with the write/admin tools (`hugo-mcp-go`), using `mcp-runtime-go` for the storage layer.

**Architecture:** Dual-server HTTP: public path (anonymous, no token) serves the 9 read-only tools; full server (same binary, different middleware chain) guards content.read/write and site.admin tools behind OAuth scope checks. A single binary replaces both `hugo-public-mcp` and `hugo-mcp-go` on `mcp.arleo.eu`.

**Tech Stack:** Go 1.25.0, `github.com/modelcontextprotocol/go-sdk v1.6.1`, `gopkg.in/yaml.v3`, `modernc.org/sqlite`, `golang.org/x/time/rate`, `golang.org/x/net`

## Global Constraints

- Module path: `github.com/jmrGrav/mcp-hugo-server-go`
- Binary name: `mcp-hugo-server-go`
- Config env var: `MCP_HUGO_SERVER_CONFIG`
- Default HTTP port: `8088`, default bind addr: `127.0.0.1`
- Site root (VM): `/home/jm/hugo-site/public`
- Systemd service name: `mcp-hugo-server-go`
- Config path on VM: `/etc/mcp-hugo-server-go/config.yaml`
- No comments unless WHY is non-obvious
- TDD: write failing test first, then implement
- No YAGNI: do not add features not in the task spec
- Each commit message must start with `feat:`, `fix:`, `chore:`, `test:`, or `security:`
- All tests must pass before commit (`go test ./...`)
- Source repos (read-only references, do not modify):
  - `hugo-public-mcp` at `/home/jm/Documents/hugo-public-mcp/`
  - `hugo-mcp-go` at `/home/jm/Documents/hugo-mcp-go/`
  - `mcp-runtime-go` at `/home/jm/Documents/mcp-runtime-go/`
- Target repo: `/home/jm/Documents/mcp-hugo-server-go/`

---

### Task 1: Package structure bootstrap

**Files:**
- Create: `go.mod`
- Create: `go.sum` (via `go mod tidy`)
- Create: `cmd/mcp-hugo-server-go/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.Config` struct (fields listed in step 3)
  - `config.Default() Config`
  - `config.Load(path string) (Config, error)`

- [ ] **Step 1: Write the failing config test**

Create `internal/config/config_test.go`:
```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := config.Default()
	if cfg.HTTPBindPort != 8088 {
		t.Fatalf("want port 8088, got %d", cfg.HTTPBindPort)
	}
	if cfg.HTTPBindAddr != "127.0.0.1" {
		t.Fatalf("want 127.0.0.1, got %s", cfg.HTTPBindAddr)
	}
	if cfg.Transport != "stdio" {
		t.Fatalf("want stdio, got %s", cfg.Transport)
	}
}

func TestLoadConfig(t *testing.T) {
	f, _ := os.CreateTemp(t.TempDir(), "config*.yaml")
	f.WriteString("http_bind_port: 9000\nsite_root: /tmp/site\n")
	f.Close()
	cfg, err := config.Load(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPBindPort != 9000 {
		t.Fatalf("want 9000, got %d", cfg.HTTPBindPort)
	}
	if cfg.SiteRoot != "/tmp/site" {
		t.Fatalf("want /tmp/site, got %s", cfg.SiteRoot)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPBindPort != 8088 {
		t.Fatalf("want 8088, got %d", cfg.HTTPBindPort)
	}
}

func TestLoadNonexistentFileErrors(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
go test ./internal/config/...
```
Expected: FAIL — package not found.

- [ ] **Step 3: Create go.mod**

```
module github.com/jmrGrav/mcp-hugo-server-go

go 1.25.0
```

Then add dependencies:
```bash
cd /home/jm/Documents/mcp-hugo-server-go
go get github.com/modelcontextprotocol/go-sdk@v1.6.1
go get gopkg.in/yaml.v3@v3.0.1
go mod tidy
```

- [ ] **Step 4: Implement `internal/config/config.go`**

```go
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SiteRoot         string      `yaml:"site_root"`
	SiteURL          string      `yaml:"site_url"`
	SiteName         string      `yaml:"site_name"`
	DefaultLanguage  string      `yaml:"language_default"`
	Transport        string      `yaml:"transport"`
	HTTPBindAddr     string      `yaml:"http_bind_addr"`
	HTTPBindPort     int         `yaml:"http_bind_port"`
	StreamingEnabled bool        `yaml:"streaming_enabled"`
	MaxIndexEntries  int         `yaml:"max_index_entries"`
	MaxResultItems   int         `yaml:"max_result_items"`
	MaxRequestBytes  int64       `yaml:"max_request_bytes"`
	RejectSymlinks   bool        `yaml:"reject_symlinks"`
	RejectHiddenPath bool        `yaml:"reject_hidden_paths"`
	OAuth            OAuthConfig `yaml:"oauth"`
}

type OAuthConfig struct {
	Enabled               bool     `yaml:"enabled"`
	Issuer                string   `yaml:"issuer"`
	Resource              string   `yaml:"resource"`
	DynamicClientEnabled  bool     `yaml:"dynamic_client_registration"`
	RequirePKCE           bool     `yaml:"require_pkce"`
	TrustedAuthorizeCIDRs []string `yaml:"trusted_authorize_cidrs"`
	AuthCodeTTLSeconds    int      `yaml:"auth_code_ttl_seconds"`
	AccessTokenTTLSeconds int      `yaml:"access_token_ttl_seconds"`
}

func Default() Config {
	return Config{
		Transport:        "stdio",
		HTTPBindAddr:     "127.0.0.1",
		HTTPBindPort:     8088,
		StreamingEnabled: true,
		DefaultLanguage:  "en",
		MaxIndexEntries:  5000,
		MaxResultItems:   50,
		MaxRequestBytes:  1 << 20,
		RejectSymlinks:   true,
		RejectHiddenPath: true,
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
	return cfg, nil
}
```

- [ ] **Step 5: Create `cmd/mcp-hugo-server-go/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-hugo-server-go: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	_ = ctx

	cfgPath := os.Getenv("MCP_HUGO_SERVER_CONFIG")
	_, err := config.Load(cfgPath)
	return err
}
```

- [ ] **Step 6: Run tests**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
go test ./...
```
Expected: all tests pass.

```bash
go build ./cmd/mcp-hugo-server-go/
```
Expected: binary produced, no errors.

- [ ] **Step 7: Commit**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
git add go.mod go.sum cmd/ internal/config/
git commit -m "chore: bootstrap module structure and config package"
```

---

### Task 2: Pathguard (write-side security)

**Files:**
- Create: `internal/security/pathguard.go`
- Create: `internal/security/pathguard_test.go`
- Source reference: `/home/jm/Documents/hugo-mcp-go/internal/pathguard/`

**Interfaces:**
- Consumes: nothing from Task 1 directly
- Produces:
  - `security.PathGuard` struct
  - `security.New(root string, rejectSymlinks bool) (*PathGuard, error)`
  - `(*PathGuard).SafeJoin(rel string) (string, error)` — resolves rel path under root, rejects symlinks if configured, rejects hidden paths, rejects path traversal
  - `(*PathGuard).WithinRoot(abs string) bool`

- [ ] **Step 1: Write failing tests**

Create `internal/security/pathguard_test.go`:
```go
package security_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/security"
)

func TestSafeJoinNormal(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "page.md"), []byte("hello"), 0644)
	pg, err := security.New(root, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pg.SafeJoin("page.md")
	if err != nil {
		t.Fatal(err)
	}
	if !pg.WithinRoot(got) {
		t.Fatal("expected path within root")
	}
}

func TestSafeJoinTraversal(t *testing.T) {
	root := t.TempDir()
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin("../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestSafeJoinHiddenPath(t *testing.T) {
	root := t.TempDir()
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin(".hidden/file")
	if err == nil {
		t.Fatal("expected error for hidden path")
	}
}

func TestSafeJoinSymlink(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(root, "link")
	os.Symlink(target, link)
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin("link")
	if err == nil {
		t.Fatal("expected error for symlink when reject_symlinks=true")
	}
}

func TestSafeJoinEmptySlug(t *testing.T) {
	root := t.TempDir()
	pg, _ := security.New(root, true)
	_, err := pg.SafeJoin("")
	if err == nil {
		t.Fatal("expected error for empty slug")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
go test ./internal/security/...
```
Expected: FAIL — package not found.

- [ ] **Step 3: Implement `internal/security/pathguard.go`**

Read the source: `/home/jm/Documents/hugo-mcp-go/internal/pathguard/pathguard.go` for reference. Adapt to this package. Key implementation:
- `SafeJoin` must reject empty slug, hidden path components (starting with `.`), and use `filepath.EvalSymlinks` when `rejectSymlinks=true`
- `WithinRoot` verifies the resulting absolute path starts with root + `/`

- [ ] **Step 4: Run tests**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
go test ./internal/security/...
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/security/
git commit -m "feat: add pathguard write-side security package"
```

---

### Task 3: Site index (in-memory HTML published pages)

**Files:**
- Create: `internal/site/types.go`
- Create: `internal/site/index.go`
- Create: `internal/site/index_test.go`
- Create: `internal/site/markdown.go`
- Source reference: `/home/jm/Documents/hugo-public-mcp/internal/site/`

**Interfaces:**
- Consumes: `config.Config` (SiteRoot, MaxIndexEntries, DefaultLanguage)
- Produces:
  - `site.Page` struct: `{Slug, Title, Summary, Tags, Categories, Date, URL, Lang string; RawHTML string}`
  - `site.Index` struct
  - `site.NewIndex(cfg config.Config) (*Index, error)` — scans SiteRoot for HTML, builds in-memory index
  - `(*Index).Search(query string, limit int) []Page`
  - `(*Index).GetBySlug(slug string) (*Page, bool)`
  - `(*Index).RecentPosts(n int) []Page`
  - `(*Index).AllTags() []string`
  - `(*Index).AllCategories() []string`
  - `(*Index).Sitemap() []Page`
  - `(*Index).GetFeed(limit int) []Page`
  - `(*Index).SiteInfo() map[string]string` — returns name, url, lang
  - `site.ExtractMarkdown(html string) string` — strips HTML tags, returns readable text

- [ ] **Step 1: Write failing tests**

Create `internal/site/index_test.go` — read `/home/jm/Documents/hugo-public-mcp/internal/site/index_test.go` for test patterns and adapt them. Key tests to include:
- `TestNewIndexEmpty`: empty dir → index with 0 pages, no error
- `TestSearchPages`: fixture with 2 HTML pages → search returns correct results
- `TestGetBySlug`: known slug → found; unknown → not found
- `TestRecentPosts`: returns pages sorted by date descending
- `TestAllTags`: returns deduplicated sorted tag list
- `TestGetBySlugEmptySlug`: empty slug → not found (no panic)

Use testdata fixtures: copy from `/home/jm/Documents/hugo-public-mcp/testdata/fixtures/public/minimal/` into `testdata/fixtures/public/minimal/` in the new repo. Only the HTML fixture files are needed for this task (not auth.md).

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
go test ./internal/site/...
```

- [ ] **Step 3: Implement site package**

Read and adapt from the source:
- `/home/jm/Documents/hugo-public-mcp/internal/site/types.go`
- `/home/jm/Documents/hugo-public-mcp/internal/site/index.go`
- `/home/jm/Documents/hugo-public-mcp/internal/site/markdown.go`

Keep the same logic; update import paths to `github.com/jmrGrav/mcp-hugo-server-go/internal/...`.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/site/...
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/site/ testdata/
git commit -m "feat: add in-memory site index for published HTML pages"
```

---

### Task 4: 9 anonymous tools

**Files:**
- Create: `internal/tools/anonymous/tools.go`
- Create: `internal/tools/anonymous/tools_test.go`
- Source reference: `/home/jm/Documents/hugo-public-mcp/internal/publicmcp/tools.go`

**Interfaces:**
- Consumes:
  - `site.Index` (from Task 3)
  - `config.Config`
- Produces:
  - `anonymous.Register(s *mcp.Server, idx *site.Index, cfg config.Config)` — registers all 9 tools on the MCP server

**Tools to register (exact names):**
1. `list_pages` — paginated list of published pages (params: `limit int`, `offset int`)
2. `get_page` — HTML page by slug (params: `slug string`)
3. `search_pages` — full-text search (params: `query string` minLength:1, `limit int` max:50)
4. `get_recent_posts` — N most recent posts (params: `limit int` default:10 max:50)
5. `list_tags` — all tags sorted
6. `list_categories` — all categories sorted
7. `get_sitemap` — all pages with URL+date
8. `get_feed` — RSS-like recent items (params: `limit int` default:20 max:50)
9. `get_site_information` — site name, URL, language

**Known bugs to fix** (from hugo-public-mcp audit):
- `search_pages.query`: enforce minLength:1 in schema (reject empty string with MCP error, not panic)
- `get_page`: empty slug → return `content_not_found` error, not empty result
- `list_pages` / `get_recent_posts` limit: cap at 50, not configurable beyond that

- [ ] **Step 1: Write failing tool tests**

Create `internal/tools/anonymous/tools_test.go`. Use `github.com/modelcontextprotocol/go-sdk/mcp` server test helpers. Key tests:
- `TestListPages`: returns page list
- `TestGetPageBySlug`: known slug → content returned
- `TestGetPageEmptySlug`: empty slug → error response, not panic
- `TestSearchPagesMinLength`: empty query → MCP error `invalid_params`
- `TestSearchPagesResults`: valid query → results
- `TestGetRecentPosts`: returns N posts sorted by date
- `TestListTags`: returns sorted tag list
- `TestGetSiteInformation`: returns name/url/lang

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/tools/anonymous/...
```

- [ ] **Step 3: Implement tools**

Read `/home/jm/Documents/hugo-public-mcp/internal/publicmcp/tools.go` and adapt. Apply the three bug fixes. Import path: `github.com/jmrGrav/mcp-hugo-server-go/...`.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/tools/anonymous/...
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/anonymous/
git commit -m "feat: implement 9 anonymous MCP tools from hugo-public-mcp with bug fixes"
```

---

### Task 5: HTTP dual-server + stub OAuth config

**Files:**
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`
- Modify: `cmd/mcp-hugo-server-go/main.go` (wire server)
- Create: `deploy/systemd/mcp-hugo-server-go.service`

**Interfaces:**
- Consumes:
  - `config.Config` (Task 1)
  - `site.Index` (Task 3)
  - `anonymous.Register` (Task 4)
- Produces:
  - `server.New(cfg config.Config, idx *site.Index) (*Server, error)`
  - `(*Server).Run(ctx context.Context) error` — starts HTTP listener, handles `/mcp` with anonymous tools, returns on ctx cancel

Architecture: single HTTP server. Public path `/mcp` serves anonymous tools (no auth check). Stub for OAuth: if `cfg.OAuth.Enabled == true`, server starts but no OAuth middleware yet (OAuth is Task 8). 404 for unknown paths.

- [ ] **Step 1: Write failing server tests**

```go
package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPEndpointResponds(t *testing.T) {
	// POST /mcp with tools/list → 200 with tools array
}

func TestUnknownPathReturns404(t *testing.T) {
	// GET /unknown → 404
}

func TestMCPMethodNotAllowed(t *testing.T) {
	// GET /mcp → 405
}
```

Use `httptest.NewServer` pattern from `/home/jm/Documents/hugo-public-mcp/internal/server/server_test.go` for reference.

- [ ] **Step 2: Implement server**

Read `/home/jm/Documents/hugo-public-mcp/internal/server/server.go` for structure. Adapt: keep only what's needed for anonymous tools + HTTP. No OAuth wiring yet.

- [ ] **Step 3: Wire main.go**

Update `cmd/mcp-hugo-server-go/main.go` to:
1. Load config
2. Build site index from `cfg.SiteRoot`
3. Create server
4. `server.Run(ctx)`

- [ ] **Step 4: Create systemd service file**

Create `deploy/systemd/mcp-hugo-server-go.service`:
```ini
[Unit]
Description=Hugo unified MCP server
Documentation=https://github.com/jmrGrav/mcp-hugo-server-go
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=mcp-hugo-server-go
Group=mcp-hugo-server-go
Environment=MCP_HUGO_SERVER_CONFIG=/etc/mcp-hugo-server-go/config.yaml
ExecStart=/usr/local/bin/mcp-hugo-server-go
Restart=on-failure
RestartSec=2
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
LockPersonality=true
MemoryDenyWriteExecute=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
RestrictNamespaces=true
RestrictSUIDSGID=true
SystemCallArchitectures=native
SystemCallFilter=@system-service
ReadOnlyPaths=/home/jm/hugo-site/public

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/server/... ./...
```
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/server/ cmd/ deploy/
git commit -m "feat: HTTP server with anonymous MCP endpoint and systemd service"
```

---

### Task 6: Agent-ready discovery endpoints + deploy to VM (replaces hugo-public-mcp)

**Files:**
- Modify: `internal/server/server.go` (add discovery routes)
- Create: `internal/server/discovery.go`
- Create: `internal/server/discovery_test.go`
- Create: `deploy/deploy.sh` (deploy script)

**Discovery endpoints to implement:**
- `GET /.well-known/oauth-authorization-server` → JSON with `issuer`, `token_endpoint`, `authorization_endpoint`, `registration_endpoint`, `grant_types_supported`, `agent_auth` block (stub: without active OAuth yet, but valid JSON structure matching `hugo-public-mcp` format)
- `GET /.well-known/oauth-protected-resource` → JSON with `resource`, `authorization_servers`
- `GET /robots.txt` → allow all bots, point to sitemap
- `GET /llms.txt` → site description for LLM agents
- `GET /auth.md` → serve auth.md from site root (read from `{SiteRoot}/auth.md`)

The `agent_auth` block in `/.well-known/oauth-authorization-server` must match exactly what `hugo-public-mcp` serves (IsItAgentReady 100/100 depends on it):
```json
{
  "identity_endpoint": "https://{issuer}/agent/identity",
  "claim_endpoint": "https://{issuer}/agent/identity/claim",
  "events_endpoint": "https://{issuer}/agent/event/notify",
  "identity_types_supported": ["anonymous"],
  "identity_assertion": {
    "assertion_types_supported": ["urn:ietf:params:oauth:token-type:id-jag"]
  },
  "events_supported": ["https://schemas.workos.com/events/agent/auth/identity/assertion/revoked"],
  "skill": "https://{issuer}/auth.md"
}
```

**Deploy script** (`deploy/deploy.sh`):
```bash
#!/usr/bin/env bash
set -euo pipefail
REMOTE="hugo-vm"
BINARY="mcp-hugo-server-go"
# build
cd "$(git rev-parse --show-toplevel)"
GOOS=linux GOARCH=amd64 go build -o "$BINARY" ./cmd/mcp-hugo-server-go/
# copy binary
scp "$BINARY" "$REMOTE:/tmp/$BINARY"
# install and switch service
ssh "$REMOTE" "sudo mv /tmp/$BINARY /usr/local/bin/$BINARY && sudo chmod 755 /usr/local/bin/$BINARY"
ssh "$REMOTE" "sudo systemctl stop hugo-public-mcp 2>/dev/null || true"
ssh "$REMOTE" "sudo systemctl disable hugo-public-mcp 2>/dev/null || true"
# copy service file if not present
scp deploy/systemd/mcp-hugo-server-go.service "$REMOTE:/tmp/mcp-hugo-server-go.service"
ssh "$REMOTE" "sudo mv /tmp/mcp-hugo-server-go.service /etc/systemd/system/mcp-hugo-server-go.service"
ssh "$REMOTE" "sudo systemctl daemon-reload && sudo systemctl enable --now mcp-hugo-server-go"
rm -f "$BINARY"
echo "Deployed. New service status:"
ssh "$REMOTE" "systemctl status mcp-hugo-server-go --no-pager | head -8"
```

**Config file on VM** — create at `/etc/mcp-hugo-server-go/config.yaml` (same values as old config):
```yaml
site_root: /home/jm/hugo-site/public
site_url: https://www.arleo.eu
site_name: arleo.eu
language_default: fr
transport: http
http_bind_addr: 192.168.122.69
http_bind_port: 8088
streaming_enabled: true
max_index_entries: 5000
max_result_items: 50
max_request_bytes: 1048576
reject_symlinks: true
reject_hidden_paths: true
oauth:
  enabled: false
```

**After deploy:** verify `curl https://mcp.arleo.eu/.well-known/oauth-authorization-server | jq .agent_auth` returns the expected block.

- [ ] **Step 1: Write failing discovery tests**

```go
func TestWellKnownOAuthServer(t *testing.T) {
	// GET /.well-known/oauth-authorization-server → 200, agent_auth block present
}
func TestWellKnownProtectedResource(t *testing.T) {
	// GET /.well-known/oauth-protected-resource → 200, resource field present
}
func TestRobotsTxt(t *testing.T) {
	// GET /robots.txt → 200, contains "User-agent: *"
}
func TestAuthMdServed(t *testing.T) {
	// GET /auth.md → 200, contains "auth.md protocol"
}
```

- [ ] **Step 2: Implement discovery.go**

- [ ] **Step 3: Run tests**

```bash
go test ./internal/server/...
```

- [ ] **Step 4: Create deploy script and commit**

```bash
git add internal/server/discovery.go internal/server/discovery_test.go deploy/
git commit -m "feat: agent-ready discovery endpoints + deploy script"
```

- [ ] **Step 5: Deploy to VM**

```bash
# First: create config and user on VM
ssh hugo-vm "sudo useradd --system --no-create-home --shell /usr/sbin/nologin mcp-hugo-server-go 2>/dev/null || true"
ssh hugo-vm "sudo mkdir -p /etc/mcp-hugo-server-go && sudo chown mcp-hugo-server-go:mcp-hugo-server-go /etc/mcp-hugo-server-go"
# Copy config
scp /tmp/mcp-hugo-server-go-config.yaml hugo-vm:/tmp/config.yaml
ssh hugo-vm "sudo mv /tmp/config.yaml /etc/mcp-hugo-server-go/config.yaml && sudo chown mcp-hugo-server-go:mcp-hugo-server-go /etc/mcp-hugo-server-go/config.yaml"
# Deploy binary and switch service
bash deploy/deploy.sh
```

- [ ] **Step 6: Verify discovery live**

```bash
curl -s https://mcp.arleo.eu/.well-known/oauth-authorization-server | python3 -m json.tool | grep -A 10 agent_auth
```

---

### Task 7: Storage interface + SQLite/JSON persistence

**Files:**
- Create: `internal/storage/store.go`
- Create: `internal/storage/sqlite.go`
- Create: `internal/storage/json.go`
- Create: `internal/storage/storage_test.go`
- Source reference: `/home/jm/Documents/mcp-runtime-go/internal/storage/`

**Interfaces:**
- Produces:
  - `storage.Store` interface:
    ```go
    type Store interface {
        AddAccessToken(token, scope string, expiresAt time.Time) error
        ValidateAccessToken(token string) (scope string, ok bool)
        PurgeExpiredTokens() error
        Close() error
    }
    ```
  - `storage.NewSQLite(path string) (Store, error)` — WAL mode, creates schema on open
  - `storage.NewJSON(path string) (Store, error)` — JSON file fallback
  - `storage.NewMemory() Store` — in-memory (for tests)

- [ ] **Step 1: Write failing tests**

Test all three backends implement the interface identically:
```go
func testStore(t *testing.T, s storage.Store) {
	t.Helper()
	defer s.Close()
	// Add token, validate → found with scope
	// Validate expired token → not found
	// PurgeExpiredTokens → removes expired
	// Validate unknown token → not found
}
func TestMemoryStore(t *testing.T) { testStore(t, storage.NewMemory()) }
func TestSQLiteStore(t *testing.T) {
	s, err := storage.NewSQLite(filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil { t.Fatal(err) }
	testStore(t, s)
}
func TestJSONStore(t *testing.T) {
	s, err := storage.NewJSON(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil { t.Fatal(err) }
	testStore(t, s)
}
```

- [ ] **Step 2: Add SQLite dependency**

```bash
go get modernc.org/sqlite@v1.51.0
go mod tidy
```

- [ ] **Step 3: Implement storage package**

Read `/home/jm/Documents/mcp-runtime-go/internal/storage/` for reference. Adapt imports. SQLite schema:
```sql
CREATE TABLE IF NOT EXISTS access_tokens (
  token TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);
```
WAL mode: `PRAGMA journal_mode=WAL`.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/storage/...
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/storage/ go.mod go.sum
git commit -m "feat: token storage with SQLite/JSON/memory backends"
```

---

### Task 8: OAuth service (PKCE, dynamic client, agent auth)

**Files:**
- Create: `internal/oauth/oauth.go`
- Create: `internal/oauth/agent_auth.go`
- Create: `internal/oauth/oauth_test.go`
- Modify: `internal/server/server.go` (wire OAuth routes when enabled)
- Source reference:
  - `/home/jm/Documents/hugo-public-mcp/internal/oauth/oauth.go`
  - `/home/jm/Documents/hugo-public-mcp/internal/oauth/agent_auth.go`

**Interfaces:**
- Consumes:
  - `config.OAuthConfig`
  - `storage.Store` (Task 7)
- Produces:
  - `oauth.Service` struct
  - `oauth.NewService(cfg config.OAuthConfig, store storage.Store) *Service`
  - `(*Service).ValidateBearer(token string) (scope string, ok bool)`
  - `(*Service).HandleRegister(w, r)` — Dynamic Client Registration
  - `(*Service).HandleAuthorize(w, r)` — Authorization Code + PKCE
  - `(*Service).HandleToken(w, r)` — token endpoint (PKCE exchange + JWT bearer)
  - `(*Service).HandleAgentIdentity(w, r)` — `POST /agent/identity`
  - `(*Service).HandleAgentClaim(w, r)` — `POST /agent/identity/claim`
  - `(*Service).HandleAgentEvent(w, r)` — `POST /agent/event/notify`
  - `(*Service).AuthorizationServerMetadata() map[string]any` — full metadata including `agent_auth` block

- [ ] **Step 1: Write failing tests**

Read `/home/jm/Documents/hugo-public-mcp/internal/server/server_test.go` for OAuth test patterns (TestAgentIdentityAnonymous, TestAgentTokenExchangeViaAssertion, etc.) and adapt. Key tests:
- `TestAgentIdentityAnonymous`: POST /agent/identity {"type":"anonymous"} → 200, identity_assertion field
- `TestAgentTokenExchange`: POST /token with jwt-bearer grant → access_token
- `TestDynamicClientRegistration`: POST /register → client_id
- `TestPKCEFlow`: full authorize+token code flow with S256
- `TestBearerValidation`: ValidateBearer with known token → scope; with expired → not found

- [ ] **Step 2: Implement oauth package**

Read and adapt from `hugo-public-mcp/internal/oauth/`. Replace in-memory token map with `storage.Store`. Keep all agent auth logic.

- [ ] **Step 3: Wire OAuth into server**

In `internal/server/server.go`: when `cfg.OAuth.Enabled == true`, register:
- `POST /register`
- `GET/POST /authorize`
- `POST /token`
- `POST /agent/identity`
- `POST /agent/identity/claim`
- `POST /agent/event/notify`

Bearer middleware: for `/mcp` on the full server path, extract `Authorization: Bearer <token>`, call `oauth.ValidateBearer`, attach scope to request context.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/oauth/... ./internal/server/...
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/ internal/server/
git commit -m "feat: OAuth service with PKCE, dynamic client, and agent auth.md protocol"
```

---

### Task 9: AnonymousMCPPolicy (ACL — public/private tool boundary)

**Files:**
- Create: `internal/oauth/acl.go`
- Create: `internal/oauth/acl_test.go`
- Modify: `internal/server/server.go` (apply ACL middleware)
- Source reference: `/home/jm/Documents/mcp-runtime-go/internal/` (look for mcp_acl.go pattern)

**Interfaces:**
- Consumes: list of public tool names (configurable)
- Produces:
  - `oauth.AnonymousMCPPolicy` struct
  - `oauth.NewACLPolicy(publicTools []string) *AnonymousMCPPolicy`
  - `(*AnonymousMCPPolicy).AllowRequest(body []byte) bool` — returns false if body contains a `tools/call` for a non-public tool
  - `(*AnonymousMCPPolicy).DenyReason(body []byte) string` — returns `"forbidden_tool"` for protected, `"unknown_tool"` for unknown

Public tools list: `["list_pages","get_page","search_pages","get_recent_posts","list_tags","list_categories","get_sitemap","get_feed","get_site_information"]`

- [ ] **Step 1: Write failing tests**

```go
func TestACLAllowsPublicTool(t *testing.T) {
	// tools/call for list_pages → allowed
}
func TestACLBlocksProtectedTool(t *testing.T) {
	// tools/call for get_full_page_markdown (not in public list) → denied, reason=forbidden_tool
}
func TestACLUnknownTool(t *testing.T) {
	// tools/call for nonexistent_tool → denied, reason=unknown_tool
}
func TestACLBatchWithForbidden(t *testing.T) {
	// batch: [list_pages, get_full_page_markdown] → denied (contains forbidden)
}
func TestACLToolsList(t *testing.T) {
	// tools/list → always allowed
}
```

- [ ] **Step 2: Implement acl.go**

Parse JSON-RPC body: extract `method` and `params.name`. For batch arrays, check each element.

- [ ] **Step 3: Apply in server**

On anonymous path (no token): apply `AnonymousMCPPolicy` before forwarding to MCP server. On authenticated path: skip ACL (scope checked by tools/list filter in Task 11).

- [ ] **Step 4: Run tests**

```bash
go test ./internal/oauth/...
```
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/oauth/acl.go internal/oauth/acl_test.go internal/server/
git commit -m "feat: AnonymousMCPPolicy ACL — distinguish forbidden vs unknown tools"
```

---

### Task 10: 5 content.read tools

**Files:**
- Create: `internal/tools/read/tools.go`
- Create: `internal/tools/read/tools_test.go`
- Source reference: `/home/jm/Documents/hugo-public-mcp/internal/publicmcp/private_tools.go`

**Interfaces:**
- Consumes: `site.Index` (Task 3)
- Produces:
  - `read.Register(s *mcp.Server, idx *site.Index, cfg config.Config)` — registers 5 tools

**Tools (exact names, scope `content.read`):**
1. `get_full_page_markdown` — params: `slug string` → full markdown-formatted content of a published page
2. `get_page_frontmatter` — params: `slug string` → structured metadata (title, date, tags, categories, reading_time)
3. `get_related_content` — params: `slug string`, `limit int` (default 5) → pages sharing tags/categories
4. `build_agent_context` — params: `slug string` → bundle: {frontmatter, markdown, related_pages}
5. `export_agent_context` — params: `tag string`, `category string`, `limit int`, `offset int` → paginated bundles

Each tool must include `RequiredScope: "content.read"` in its description metadata (will be used by Task 11's filter).

- [ ] **Step 1: Write failing tests**

Pattern from `/home/jm/Documents/hugo-public-mcp/internal/publicmcp/private_tools_test.go`. Key tests:
- `TestGetFullPageMarkdown`: known slug → markdown content
- `TestGetFullPageMarkdownUnknown`: unknown slug → not_found error
- `TestGetPageFrontmatter`: known slug → reading_time > 0
- `TestGetRelatedContent`: page with tags → returns related pages
- `TestBuildAgentContext`: returns bundle with all 3 fields
- `TestExportAgentContext`: paginates correctly

- [ ] **Step 2: Implement tools**

Adapt from `/home/jm/Documents/hugo-public-mcp/internal/publicmcp/private_tools.go`. Add `RequiredScope` annotation.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/tools/read/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/tools/read/
git commit -m "feat: implement 5 content.read tools with RequiredScope annotation"
```

---

### Task 11: Dynamic tools/list filtering by OAuth scope

**Files:**
- Create: `internal/tools/registry.go`
- Create: `internal/tools/registry_test.go`
- Modify: `internal/server/server.go` (use registry for tools/list)

**Interfaces:**
- Consumes: tool definitions from Tasks 4 and 10
- Produces:
  - `tools.ToolDef` struct: `{Name, Description, RequiredScope string; InputSchema any}`
  - `tools.Registry` struct
  - `tools.NewRegistry() *Registry`
  - `(*Registry).Register(def ToolDef)`
  - `(*Registry).ForScope(scope string) []ToolDef` — returns tools where `RequiredScope == "" || RequiredScope == scope` or scope is a "higher" tier

Scope hierarchy (higher includes lower):
```
anonymous ("") < content.read < content.write < site.admin
```

`ForScope("content.read")` returns anonymous + content.read tools.
`ForScope("site.admin")` returns anonymous + content.read + content.write + site.admin tools.
`ForScope("")` (no token) returns only anonymous tools.

- [ ] **Step 1: Write failing tests**

```go
func TestAnonymousScopeSeesOnlyPublicTools(t *testing.T) {
	// ForScope("") → 9 tools
}
func TestContentReadScopeSeesReadTools(t *testing.T) {
	// ForScope("content.read") → 14 tools (9 + 5)
}
func TestScopeInclusion(t *testing.T) {
	// ForScope("site.admin") includes content.write tools
}
```

- [ ] **Step 2: Implement registry**

- [ ] **Step 3: Wire into server**

In the `tools/list` handler: extract scope from request context (set by bearer middleware), call `registry.ForScope(scope)`, return filtered list.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/tools/... ./internal/server/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/tools/registry.go internal/tools/registry_test.go internal/server/
git commit -m "feat: dynamic tools/list filtering by OAuth scope tier"
```

---

### Task 12: Hugo source index (Markdown content/ layer)

**Files:**
- Create: `internal/hugosite/source_index.go`
- Create: `internal/hugosite/source_index_test.go`
- Source reference: `/home/jm/Documents/hugo-mcp-go/internal/hugo/`

**Interfaces:**
- Consumes: `config.Config` (need a `ContentRoot` field — add to config: `ContentRoot string yaml:"content_root"`)
- Produces:
  - `hugosite.SourcePage` struct: `{Slug, Title, Date, Draft bool; Tags, Categories []string; Body string; FrontmatterRaw map[string]any}`
  - `hugosite.SourceIndex` struct
  - `hugosite.NewSourceIndex(contentRoot string) (*SourceIndex, error)` — scans `content/` for `.md` files
  - `(*SourceIndex).GetBySlug(slug string) (*SourcePage, bool)`
  - `(*SourceIndex).AllSlugs() []string`
  - `(*SourceIndex).ListPages(limit, offset int) []SourcePage`

**Naming:** `list_source_pages` and `get_source_page` (NOT `list_pages`/`get_page` — those are the published HTML tools in Task 4).

- [ ] **Step 1: Extend config**

Add `ContentRoot string yaml:"content_root"` to `config.Config` in `internal/config/config.go`. No default (empty = feature disabled). Update config tests.

- [ ] **Step 2: Write failing source index tests**

Use testdata: create `testdata/fixtures/content/` with 2-3 sample `.md` files with YAML frontmatter.

- [ ] **Step 3: Implement source index**

Read and adapt from `/home/jm/Documents/hugo-mcp-go/internal/hugo/` (pages and frontmatter packages). Parse YAML frontmatter between `---` delimiters.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/hugosite/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/hugosite/ internal/config/ testdata/fixtures/content/
git commit -m "feat: Hugo Markdown source index for content/ directory"
```

---

### Task 13: Write tools — create_page, update_page, delete_page

**Files:**
- Create: `internal/tools/write/tools.go`
- Create: `internal/tools/write/tools_test.go`
- Source reference: `/home/jm/Documents/hugo-mcp-go/internal/tools/mutations/`

**Interfaces:**
- Consumes:
  - `security.PathGuard` (Task 2)
  - `hugosite.SourceIndex` (Task 12)
  - `config.Config`
- Produces:
  - `write.Register(s *mcp.Server, pg *security.PathGuard, idx *hugosite.SourceIndex, cfg config.Config)`

**Tools (scope `content.write`):**
1. `create_page` — params: `slug string`, `title string`, `body string`, `tags []string`, `categories []string`
   - Writes `{ContentRoot}/{slug}/index.md` atomically (tmp file + rename)
   - Validates: slug not empty, title not empty
   - Rejects reserved slugs: `_index`, `index`
2. `update_page` — params: `slug string`, `title string` (optional), `body string` (optional)
   - Must find existing page; error if not found
   - Atomic write
3. `delete_page` — params: `slug string`
   - Rate limited: max 5 per minute (in-process token bucket, `golang.org/x/time/rate`)
   - Audit log: append to `{ContentRoot}/.mcp-audit.log` with timestamp + slug

**Security (all tools):**
- All file paths through `pg.SafeJoin(slug)` — path traversal rejected
- Atomic write: write to `path + ".tmp"` then `os.Rename`

- [ ] **Step 1: Add rate limiting dependency**

```bash
go get golang.org/x/time/rate
go mod tidy
```

- [ ] **Step 2: Write failing tests**

```go
func TestCreatePage(t *testing.T) {
	// creates file at correct path with frontmatter
}
func TestCreatePageSymlinkBlocked(t *testing.T) {
	// slug that resolves to symlink → error
}
func TestCreatePageReservedSlug(t *testing.T) {
	// slug="_index" → error
}
func TestDeletePageRateLimit(t *testing.T) {
	// 6 deletes in a row → 6th returns rate_limit_exceeded error
}
func TestUpdatePageNotFound(t *testing.T) {
	// update nonexistent slug → not_found error
}
```

- [ ] **Step 3: Implement write tools**

- [ ] **Step 4: Run tests**

```bash
go test ./internal/tools/write/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/tools/write/ go.mod go.sum
git commit -m "feat: content.write tools with pathguard, atomic writes, and delete rate limiting"
```

---

### Task 14: generate_featured_image tool (site.admin)

**Files:**
- Create: `internal/tools/admin/image.go`
- Create: `internal/tools/admin/image_test.go`
- Source reference: `/home/jm/Documents/hugo-mcp-go/internal/tools/tools.go` (look for image generation)

**Tool:** `generate_featured_image` — scope `site.admin`
- Params: `slug string`, `prompt string`
- Calls configured image generation API (URL + key from config: add `ImageGenURL string`, `ImageGenKey string` to `config.Config`)
- Validates response Content-Type is `image/*` before saving
- Saves to `{SiteRoot}/images/featured/{slug}.jpg` via PathGuard
- Returns path of saved image

- [ ] **Step 1: Extend config with image generation fields**

Add to `config.Config`:
```go
ImageGenURL string `yaml:"image_gen_url"`
ImageGenKey string `yaml:"image_gen_key"`
```

- [ ] **Step 2: Write failing tests**

Use `httptest.NewServer` to mock image generation API:
- Mock returns `Content-Type: image/jpeg` + fake bytes → success
- Mock returns `Content-Type: text/html` → MIME validation error
- Mock times out → timeout error

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run tests**

```bash
go test ./internal/tools/admin/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/tools/admin/image.go internal/tools/admin/image_test.go internal/config/
git commit -m "feat: generate_featured_image tool with MIME validation"
```

---

### Task 15: build_site tool with concurrent-build guard (site.admin)

**Files:**
- Create: `internal/tools/admin/build.go`
- Create: `internal/tools/admin/build_test.go`
- Source reference: `/home/jm/Documents/hugo-mcp-go/internal/runner/`

**Tool:** `build_site` — scope `site.admin`
- Params: none (uses `cfg.SiteRoot`'s parent as Hugo root)
- Global mutex: if a build is in progress, return `{"error":"build_in_progress"}` immediately (do not queue)
- Timeout: configurable via `config.Config.BuildTimeoutSeconds int yaml:"build_timeout_seconds"` (default 120)
- Runs `hugo` binary via `exec.CommandContext`
- Returns `{"status":"ok","duration_ms":N}` on success
- Structured log: duration, exit code

- [ ] **Step 1: Write failing tests**

```go
func TestBuildSiteSucceeds(t *testing.T) {
	// mock hugo binary via PATH trick → returns 0 → success response
}
func TestBuildSiteConcurrentReject(t *testing.T) {
	// trigger two builds concurrently → second returns build_in_progress
}
func TestBuildSiteTimeout(t *testing.T) {
	// mock hugo that sleeps > timeout → timeout error
}
```

- [ ] **Step 2: Implement with sync.Mutex**

```go
var buildMu sync.Mutex
var buildInProgress bool
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/tools/admin/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/tools/admin/build.go internal/tools/admin/build_test.go
git commit -m "feat: build_site tool with concurrent-build guard and timeout"
```

---

### Task 16: run_post_build_hooks tool with URL allowlist (site.admin)

**Files:**
- Create: `internal/tools/admin/hooks.go`
- Create: `internal/tools/admin/hooks_test.go`
- Source reference: `/home/jm/Documents/hugo-mcp-go/internal/hooks/`

**Tool:** `run_post_build_hooks` — scope `site.admin`
- Config: add `PostBuildHooks []string yaml:"post_build_hooks"` to `config.Config` (list of allowed URLs)
- Params: none (fires all configured hooks)
- For each hook URL: POST with `Content-Type: application/json`, body `{"event":"post_build"}`, timeout 10s
- Rejects any URL not in `cfg.PostBuildHooks` allowlist
- Returns per-hook status: `{"url": "...", "status": 200}` or `{"url": "...", "error": "..."}` for each

- [ ] **Step 1: Write failing tests**

```go
func TestHookAllowlisted(t *testing.T) {
	// URL in config → fired, returns 200 status
}
func TestHookNotAllowlisted(t *testing.T) {
	// URL not in config → rejected with SSRF error
}
func TestHookTimeout(t *testing.T) {
	// hook server hangs → timeout error in result, does not block forever
}
```

- [ ] **Step 2: Implement**

- [ ] **Step 3: Run tests**

```bash
go test ./internal/tools/admin/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/tools/admin/hooks.go internal/tools/admin/hooks_test.go
git commit -m "feat: run_post_build_hooks tool with URL allowlist SSRF protection"
```

---

### Task 17: check_sri_versions tool (site.admin)

**Files:**
- Create: `internal/tools/admin/sri.go`
- Create: `internal/tools/admin/sri_test.go`
- Source reference: `/home/jm/Documents/hugo-mcp-go/internal/sri/`

**Tool:** `check_sri_versions` — scope `site.admin`
- Scans Hugo templates in `{HugoRoot}/layouts/` for `integrity="sha384-..."` attributes
- For each CDN URL found: fetches current version, computes SHA-384, compares to template
- Returns list: `[{"url":"...", "template_hash":"...","current_hash":"...","match":true/false}]`
- Config: add `HugoRoot string yaml:"hugo_root"` to `config.Config`

- [ ] **Step 1: Write failing tests**

Use `httptest.NewServer` to mock CDN URLs. Test: matching hash → match:true; different hash → match:false.

- [ ] **Step 2: Implement**

Read `/home/jm/Documents/hugo-mcp-go/internal/sri/` for reference. Use `crypto/sha512` for SHA-384.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/tools/admin/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/tools/admin/sri.go internal/tools/admin/sri_test.go internal/config/
git commit -m "feat: check_sri_versions tool for CDN integrity verification"
```

---

### Task 18: Security audit checklist + fixes

**Files:**
- Modify: various files as findings require
- Create: `internal/tools/admin/audit_log.go` (if not already created in Task 13)

This task is an audit pass — read all security-sensitive code and fix issues found. Do NOT add new features. Fixes only.

**Checklist:**
- [ ] Verify PathGuard applied on ALL tools in write/ and admin/ packages
- [ ] Verify `delete_page` audit log appends correctly (not overwrites)
- [ ] Verify `upload_asset` (if implemented) validates MIME type
- [ ] Verify `build_site` mutex cannot deadlock (check all return paths release lock)
- [ ] Verify `run_post_build_hooks` allowlist check is first, before any HTTP call
- [ ] Verify errors in all write/admin tools never expose filesystem paths in the message
- [ ] Verify `get_source_page` rejects empty slug (no panic, returns `not_found`)
- [ ] Run `go vet ./...` — fix all issues
- [ ] Run `go test -race ./...` — fix all race conditions

- [ ] **Step 1: Run audit tools**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
go vet ./...
go test -race ./...
```

- [ ] **Step 2: Fix all findings**

For each issue: fix, then re-run the specific test covering it.

- [ ] **Step 3: Commit fixes**

```bash
git add -u
git commit -m "security: audit pass — path exposure, mutex safety, MIME validation"
```

---

### Task 19: Structured logging + Prometheus metrics middleware

**Files:**
- Create: `internal/observability/logger.go`
- Create: `internal/observability/metrics.go`
- Create: `internal/observability/middleware.go`
- Create: `internal/observability/observability_test.go`
- Modify: `internal/server/server.go` (wrap handler with middleware)
- Source reference: `/home/jm/Documents/hugo-public-mcp/internal/observability/`

**Interfaces:**
- Produces:
  - `observability.NewLogger() *slog.Logger`
  - `observability.RequestMiddleware(next http.Handler, log *slog.Logger) http.Handler` — logs each request: method, path, status, duration (JSON)
  - `observability.Metrics` struct with Prometheus counters (optional: only if `cfg.MetricsEnabled bool yaml:"metrics_enabled"`)
  - `GET /metrics` endpoint (if metrics enabled)

- [ ] **Step 1: Write failing tests**

```go
func TestRequestMiddlewareLogsRequest(t *testing.T) {
	// middleware logs method, path, status to a captured writer
}
func TestMetricsEndpoint(t *testing.T) {
	// if metrics enabled: GET /metrics → 200 with prometheus text
}
```

- [ ] **Step 2: Implement**

Use `log/slog` (stdlib, Go 1.21+). For Prometheus: use standard `net/http/pprof` pattern or add `github.com/prometheus/client_golang` only if user confirms (check if already in go.sum of source repos first — it's not, so skip Prometheus for now, just use slog + a simple counter map).

Actually: use only stdlib slog + a simple in-process counter map for now. No Prometheus dependency — that's YAGNI.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/observability/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/observability/
git commit -m "feat: structured slog request logging middleware"
```

---

### Task 20: Rate limiting middleware (token bucket per scope)

**Files:**
- Create: `internal/oauth/ratelimit.go`
- Create: `internal/oauth/ratelimit_test.go`
- Modify: `internal/server/server.go` (apply rate limit middleware)

**Interfaces:**
- Consumes: `golang.org/x/time/rate` (already added in Task 13)
- Produces:
  - `oauth.RateLimiter` struct
  - `oauth.NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter`
  - `(*RateLimiter).Allow(scope string) bool`
  - HTTP middleware: on 429, write `{"error":"rate_limit_exceeded"}` + `Retry-After` header

Add to `config.Config`:
```go
RateLimit RateLimitConfig `yaml:"rate_limit"`
```
```go
type RateLimitConfig struct {
	AnonymousPerMin    int `yaml:"anonymous_per_min"`    // default 60
	ContentReadPerMin  int `yaml:"content_read_per_min"` // default 120
	ContentWritePerMin int `yaml:"content_write_per_min"` // default 30
	SiteAdminPerMin    int `yaml:"site_admin_per_min"`   // default 10
	DestructivePerMin  int `yaml:"destructive_per_min"`  // default 5
}
```

- [ ] **Step 1: Write failing tests**

```go
func TestRateLimiterAllows(t *testing.T) {
	// first N requests within limit → Allow() true
}
func TestRateLimiterBlocks(t *testing.T) {
	// N+1 requests → Allow() false
}
func TestRateLimiter429Response(t *testing.T) {
	// HTTP middleware: over-limit → 429 + Retry-After header
}
```

- [ ] **Step 2: Implement**

Use `golang.org/x/time/rate.NewLimiter` per scope string. Create a limiter per unique scope on first use.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/oauth/... ./internal/server/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/oauth/ratelimit.go internal/oauth/ratelimit_test.go internal/server/ internal/config/
git commit -m "feat: per-scope rate limiting with 429 + Retry-After"
```

---

### Task 21: Deploy full server to VM and disable hugo-public-mcp

**Files:**
- Modify: `deploy/deploy.sh` (already created in Task 6; update for new features)
- Create: `deploy/config-production.yaml` (reference config, no secrets)

This task is the final deployment cutover for the new binary (now with OAuth + write tools). Pre-conditions: Task 18 security audit passed.

- [ ] **Step 1: Verify all tests pass**

```bash
cd /home/jm/Documents/mcp-hugo-server-go
go test ./...
go vet ./...
```

- [ ] **Step 2: Build and deploy**

```bash
bash deploy/deploy.sh
```

This stops `hugo-public-mcp` and starts `mcp-hugo-server-go` (deploy.sh already handles this).

- [ ] **Step 3: Smoke test live endpoints**

```bash
curl -s https://mcp.arleo.eu/.well-known/oauth-authorization-server | python3 -m json.tool | grep agent_auth
curl -s -X POST https://mcp.arleo.eu/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' | python3 -m json.tool | grep -c '"name"'
```
Expected: agent_auth block present, 9 tools listed.

- [ ] **Step 4: Deprecation notices**

Update `README.md` of `hugo-public-mcp` and `hugo-mcp-go`:
```
> **Deprecated**: This repo is superseded by [mcp-hugo-server-go](https://github.com/jmrGrav/mcp-hugo-server-go).
> Service moved to mcp.arleo.eu. This repo is archived.
```

Do this via `gh api` to avoid checking out those repos:
```bash
gh api repos/jmrGrav/hugo-public-mcp --method PATCH -f description="[DEPRECATED] Superseded by mcp-hugo-server-go"
gh api repos/jmrGrav/hugo-mcp-go --method PATCH -f description="[DEPRECATED] Superseded by mcp-hugo-server-go"
```

- [ ] **Step 5: Commit**

```bash
git add deploy/
git commit -m "chore: production deploy config and cutover from hugo-public-mcp"
```

---

### Task 22: CI/CD — GitHub Actions + test coverage gate

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Write CI workflow**

```yaml
name: CI
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'
      - run: go test -cover -coverprofile=coverage.out ./...
      - run: go vet ./...
      - name: Coverage gate
        run: |
          COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | tr -d '%')
          echo "Coverage: $COVERAGE%"
          awk "BEGIN{if($COVERAGE < 80) {print \"Coverage below 80%\"; exit 1}}"
```

- [ ] **Step 2: Push workflow**

```bash
mkdir -p .github/workflows
git add .github/workflows/ci.yml
git commit -m "chore: GitHub Actions CI with coverage gate ≥ 80%"
git push origin main
```

- [ ] **Step 3: Verify CI passes**

```bash
gh run list --limit 3
```

---

### Task 23: Final docs — auth.md update + operator guide

**Files:**
- Modify: `/home/jm/hugo-site/public/auth.md` (update endpoint references)
- Create: `docs/operator-guide.md`

- [ ] **Step 1: Update auth.md**

The live auth.md at `/home/jm/hugo-site/public/auth.md` already references `mcp.arleo.eu`. Verify the 5 authenticated tools match Task 10's tool names exactly. Update if needed.

- [ ] **Step 2: Create operator guide**

Document:
- Environment variable: `MCP_HUGO_SERVER_CONFIG`
- Config fields (all YAML keys with types and defaults)
- Scope tiers and which tools are in each
- Deploy procedure (`bash deploy/deploy.sh`)
- How to add new post-build hooks (edit `post_build_hooks:` in config)

- [ ] **Step 3: Commit**

```bash
git add docs/operator-guide.md
git commit -m "docs: operator guide and auth.md verification"
```
