package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/buildinfo"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-hugo-server-go: %s\n", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	for _, a := range args {
		if a == "--version" || a == "-version" || a == "version" {
			fmt.Printf("mcp-hugo-server-go %s\n", buildinfo.Version)
			return nil
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfgPath := os.Getenv("MCP_HUGO_SERVER_CONFIG")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	if strings.TrimSpace(cfg.SiteRoot) == "" {
		return fmt.Errorf("site_root not configured")
	}

	idx, err := site.NewIndex(cfg)
	if err != nil {
		return err
	}

	// stdio is the trusted-local-single-user transport (MCPB/desktop use,
	// #782 Phase 2): no HTTP, no OAuth, no listening socket at all — just
	// this process's own stdin/stdout talking to whatever spawned it.
	// http_bind_addr/http_bind_port and the oauth block are ignored in this
	// mode; they only apply to transport: http.
	if cfg.Transport == "stdio" {
		srv, err := server.NewStdio(cfg, idx)
		if err != nil {
			return err
		}
		return srv.Run(ctx, &mcp.StdioTransport{})
	}

	srv, err := server.New(cfg, idx)
	if err != nil {
		return err
	}

	return srv.Run(ctx)
}
