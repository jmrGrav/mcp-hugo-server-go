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
	ID           string   `yaml:"id"`
	ClientID     string   `yaml:"client_id"`
	Secret       string   `yaml:"secret,omitempty"`
	ClientSecret string   `yaml:"client_secret,omitempty"`
	SecretHash   string   `yaml:"client_secret_hash,omitempty"`
	RedirectURIs []string `yaml:"redirect_uris"`
	Scopes       []string `yaml:"scopes"`
	Scope        string   `yaml:"scope"`
	Enabled      *bool    `yaml:"enabled,omitempty"`
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
	for i, c := range file.Clients {
		file.Clients[i].ID = strings.TrimSpace(c.ID)
		file.Clients[i].ClientID = strings.TrimSpace(c.ClientID)
		file.Clients[i].Secret = strings.TrimSpace(c.Secret)
		file.Clients[i].ClientSecret = strings.TrimSpace(c.ClientSecret)
		file.Clients[i].SecretHash = strings.TrimSpace(c.SecretHash)
		file.Clients[i].Scope = strings.TrimSpace(c.Scope)
		for j := range file.Clients[i].RedirectURIs {
			file.Clients[i].RedirectURIs[j] = strings.TrimSpace(file.Clients[i].RedirectURIs[j])
		}
		for j := range file.Clients[i].Scopes {
			file.Clients[i].Scopes[j] = strings.TrimSpace(file.Clients[i].Scopes[j])
		}
	}
	return &file, nil
}
