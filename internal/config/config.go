package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SiteRoot         string      `yaml:"site_root"`
	HugoRoot         string      `yaml:"hugo_root"`
	ContentRoot      string      `yaml:"content_root"`
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
	ImageGenURL            string      `yaml:"image_gen_url"`
	ImageGenKey            string      `yaml:"image_gen_key"`
	BuildTimeoutSeconds    int         `yaml:"build_timeout_seconds"`
	PostBuildHooks         []string    `yaml:"post_build_hooks"`
	OAuth                  OAuthConfig `yaml:"oauth"`
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
		MaxIndexEntries:     5000,
		MaxResultItems:      50,
		MaxRequestBytes:     1 << 20,
		RejectSymlinks:      true,
		RejectHiddenPath:    true,
		BuildTimeoutSeconds: 120,
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
