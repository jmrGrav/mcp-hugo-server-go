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
