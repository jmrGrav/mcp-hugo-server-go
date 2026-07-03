package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type jsonEntry struct {
	Scope     string `json:"scope"`
	ExpiresAt int64  `json:"expires_at"`
}

type jsonStore struct {
	mu       sync.Mutex
	filePath string
	tokens   map[string]jsonEntry
}

func NewJSON(path string) (Store, error) {
	s := &jsonStore{
		filePath: path,
		tokens:   make(map[string]jsonEntry),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *jsonStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read json store: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &s.tokens)
}

func (s *jsonStore) save() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(s.tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, s.filePath)
}

func (s *jsonStore) AddAccessToken(token, scope string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = jsonEntry{Scope: scope, ExpiresAt: expiresAt.Unix()}
	return s.save()
}

func (s *jsonStore) ValidateAccessToken(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.tokens[token]
	if !ok || time.Now().Unix() >= e.ExpiresAt {
		return "", false
	}
	return e.Scope, true
}

func (s *jsonStore) PurgeExpiredTokens() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	for k, e := range s.tokens {
		if now >= e.ExpiresAt {
			delete(s.tokens, k)
		}
	}
	return s.save()
}

func (s *jsonStore) Close() error {
	return nil
}
