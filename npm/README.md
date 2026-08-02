# @jmrgrav/mcp-hugo-server-go

npm wrapper around [`mcp-hugo-server-go`](https://github.com/jmrGrav/mcp-hugo-server-go), a Model Context Protocol server for managing a Hugo static site.

This package contains no server logic of its own. It follows the [esbuild](https://github.com/evanw/esbuild)/[ripgrep](https://github.com/microsoft/vscode-ripgrep) pattern: on install, its `postinstall` script downloads the precompiled Go binary matching this package's own version from [GitHub Releases](https://github.com/jmrGrav/mcp-hugo-server-go/releases), verifies its SHA-256 checksum against the release's published `checksums.txt`, and the `bin/mcp-hugo-server-go` command is a thin shim that execs it with `stdio` inherited untouched.

## Install

```sh
npm install -g @jmrgrav/mcp-hugo-server-go
# or, without installing:
npx @jmrgrav/mcp-hugo-server-go
```

Supported platforms: macOS (x64/arm64), Linux (x64/arm64), Windows (x64).

## Configuration

This runs the server in `stdio` transport mode — see the main repository's [`## Installation`](https://github.com/jmrGrav/mcp-hugo-server-go#installation) section for the two transport modes and which one applies to you, and [`docs/operator-guide.md`](https://github.com/jmrGrav/mcp-hugo-server-go/blob/main/docs/operator-guide.md) for the full config reference. Configuration is via the `MCP_HUGO_SITE_ROOT` / `MCP_HUGO_HUGO_ROOT` / `MCP_HUGO_CONTENT_ROOT` / `MCP_HUGO_SITE_URL` / `MCP_HUGO_SITE_NAME` environment variables, or a `config.yaml` passed via CLI flag — see the operator guide.

## Privacy

Same as the underlying binary's stdio mode — see the main repository's [Privacy policy](https://github.com/jmrGrav/mcp-hugo-server-go#privacy-policy) section. No telemetry, no data leaves your machine except through integrations you explicitly configure.

## License

MIT — see [LICENSE](https://github.com/jmrGrav/mcp-hugo-server-go/blob/main/LICENSE).
