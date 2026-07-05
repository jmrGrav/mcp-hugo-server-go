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

# Install service file and override example only on first deploy.
# On upgrades only the binary is updated; existing .service customizations
# (ReadWritePaths, Environment, etc.) are preserved via the drop-in override.
scp deploy/systemd/mcp-hugo-server-go.service "$REMOTE:/tmp/mcp-hugo-server-go.service"
scp deploy/systemd/override.conf.example "$REMOTE:/tmp/mcp-hugo-server-go-override.conf.example"
ssh "$REMOTE" bash -s <<'ENDSSH'
if [ ! -f /etc/systemd/system/mcp-hugo-server-go.service ]; then
  sudo mv /tmp/mcp-hugo-server-go.service /etc/systemd/system/mcp-hugo-server-go.service
  sudo chmod 644 /etc/systemd/system/mcp-hugo-server-go.service
  echo "Installed service file (first deploy)."
else
  rm -f /tmp/mcp-hugo-server-go.service
  echo "Preserving existing service file."
fi
sudo mkdir -p /etc/systemd/system/mcp-hugo-server-go.service.d
if [ ! -f /etc/systemd/system/mcp-hugo-server-go.service.d/override.conf ]; then
  sudo cp /tmp/mcp-hugo-server-go-override.conf.example \
    /etc/systemd/system/mcp-hugo-server-go.service.d/override.conf
  echo "Installed override.conf example — edit it to match your site paths."
else
  echo "Preserving existing override.conf."
fi
rm -f /tmp/mcp-hugo-server-go-override.conf.example
ENDSSH

ssh "$REMOTE" "sudo systemctl daemon-reload && sudo systemctl enable mcp-hugo-server-go && sudo systemctl restart mcp-hugo-server-go"

rm -f "$BINARY"

echo "Deployed. New service status:"
ssh "$REMOTE" "systemctl status mcp-hugo-server-go --no-pager | head -8"
