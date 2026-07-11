package config_test

import (
	"testing"

	"github.com/jmrGrav/mcp-hugo-server-go/internal/config"
)

func TestEnabledHelpers(t *testing.T) {
	t.Run("Cloudflare", func(t *testing.T) {
		if (config.CloudflareConfig{}).Enabled() {
			t.Fatal("zero Cloudflare config should be disabled")
		}
		if !(config.CloudflareConfig{ZoneID: "zone", APIToken: "token"}).Enabled() {
			t.Fatal("Cloudflare config with zone+token should be enabled")
		}
	})

	t.Run("IndexNow", func(t *testing.T) {
		if (config.IndexNowConfig{}).Enabled() {
			t.Fatal("zero IndexNow config should be disabled")
		}
		if !(config.IndexNowConfig{Key: "key"}).Enabled() {
			t.Fatal("IndexNow config with key should be enabled")
		}
	})

	t.Run("GoogleIndex", func(t *testing.T) {
		if (config.GoogleIndexConfig{}).Enabled() {
			t.Fatal("zero GoogleIndex config should be disabled")
		}
		if !(config.GoogleIndexConfig{ServiceAccountPath: "/tmp/sa.json"}).Enabled() {
			t.Fatal("GoogleIndex config with service account path should be enabled")
		}
	})
}
