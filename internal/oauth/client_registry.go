package oauth

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type clientRegistryFile struct {
	Clients []clientRegistryEntry `yaml:"clients"`
}

type clientRegistryEntry struct {
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret,omitempty"`
	SecretHash   string   `yaml:"client_secret_hash,omitempty"`
	RedirectURIs []string `yaml:"redirect_uris"`
	Scope        string   `yaml:"scope"`
}

func loadClientRegistry(path string) (*clientRegistryFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read client registry: %w", err)
	}
	var file clientRegistryFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse client registry: %w", err)
	}
	if len(file.Clients) == 0 {
		return &file, nil
	}
	for i, c := range file.Clients {
		file.Clients[i].ClientID = strings.TrimSpace(c.ClientID)
		file.Clients[i].ClientSecret = strings.TrimSpace(c.ClientSecret)
		file.Clients[i].SecretHash = strings.TrimSpace(c.SecretHash)
		file.Clients[i].Scope = strings.TrimSpace(c.Scope)
	}
	return &file, nil
}
