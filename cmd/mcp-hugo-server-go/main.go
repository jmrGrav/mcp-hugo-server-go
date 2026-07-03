package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/server"
	"github.com/jmrGrav/mcp-hugo-server-go/internal/site"
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

	srv, err := server.New(cfg, idx)
	if err != nil {
		return err
	}

	return srv.Run(ctx)
}
