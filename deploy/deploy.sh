#!/usr/bin/env bash
set -euo pipefail

REMOTE="hugo-vm"
BINARY="mcp-hugo-server-go"

cd "$(git rev-parse --show-toplevel)"

VERSION=$(git describe --tags --always 2>/dev/null || echo dev)
GOOS=linux GOARCH=amd64 go build \
  -ldflags "-X github.com/jmrGrav/mcp-hugo-server-go/internal/server.Version=${VERSION}" \
  -o "$BINARY" ./cmd/mcp-hugo-server-go/

scp "$BINARY" "$REMOTE:/tmp/$BINARY"

ssh "$REMOTE" "sudo mv /tmp/$BINARY /usr/local/bin/$BINARY && sudo chmod 755 /usr/local/bin/$BINARY"

ssh "$REMOTE" "sudo systemctl stop hugo-public-mcp 2>/dev/null || true"
ssh "$REMOTE" "sudo systemctl disable hugo-public-mcp 2>/dev/null || true"

scp deploy/systemd/mcp-hugo-server-go.service "$REMOTE:/tmp/mcp-hugo-server-go.service"
ssh "$REMOTE" "sudo mv /tmp/mcp-hugo-server-go.service /etc/systemd/system/mcp-hugo-server-go.service"

ssh "$REMOTE" "sudo systemctl daemon-reload && sudo systemctl enable mcp-hugo-server-go && sudo systemctl restart mcp-hugo-server-go"

rm -f "$BINARY"

echo "Deployed. New service status:"
ssh "$REMOTE" "systemctl status mcp-hugo-server-go --no-pager | head -8"
