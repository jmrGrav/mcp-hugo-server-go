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

type jsonRefreshEntry struct {
	ClientID  string `json:"client_id"`
	Scope     string `json:"scope"`
	ExpiresAt int64  `json:"expires_at"`
}

type jsonState struct {
	AccessTokens  map[string]jsonEntry        `json:"access_tokens"`
	RefreshTokens map[string]jsonRefreshEntry `json:"refresh_tokens,omitempty"`
}

type jsonStore struct {
	mu            sync.Mutex
	filePath      string
	tokens        map[string]jsonEntry
	refreshTokens map[string]jsonRefreshEntry
}

func NewJSON(path string) (Store, error) {
	s := &jsonStore{
		filePath:      path,
		tokens:        make(map[string]jsonEntry),
		refreshTokens: make(map[string]jsonRefreshEntry),
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
	var state jsonState
	if err := json.Unmarshal(data, &state); err == nil && state.AccessTokens != nil {
		s.tokens = state.AccessTokens
		if state.RefreshTokens != nil {
			s.refreshTokens = state.RefreshTokens
		}
		return nil
	}
	var legacy map[string]jsonEntry
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	s.tokens = legacy
	return nil
}

func (s *jsonStore) save() error {
	return saveJSONState(s.filePath, jsonState{
		AccessTokens:  s.tokens,
		RefreshTokens: s.refreshTokens,
	})
}

func saveJSONState(path string, state jsonState) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, path)
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

func (s *jsonStore) AddRefreshToken(token, clientID, scope string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshTokens[token] = jsonRefreshEntry{ClientID: clientID, Scope: scope, ExpiresAt: expiresAt.Unix()}
	return s.save()
}

func (s *jsonStore) ValidateRefreshToken(token, clientID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.refreshTokens[token]
	if !ok || e.ClientID != clientID || time.Now().Unix() >= e.ExpiresAt {
		return "", false
	}
	return e.Scope, true
}

func (s *jsonStore) ExchangeRefreshToken(oldToken, clientID, newRefreshToken, newAccessToken string, accessExpiresAt, refreshExpiresAt time.Time) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.refreshTokens[oldToken]
	if !ok || e.ClientID != clientID || time.Now().Unix() >= e.ExpiresAt {
		return "", false, nil
	}
	nextTokens := make(map[string]jsonEntry, len(s.tokens)+1)
	for k, v := range s.tokens {
		nextTokens[k] = v
	}
	nextRefresh := make(map[string]jsonRefreshEntry, len(s.refreshTokens))
	for k, v := range s.refreshTokens {
		nextRefresh[k] = v
	}
	delete(nextRefresh, oldToken)
	nextTokens[newAccessToken] = jsonEntry{Scope: e.Scope, ExpiresAt: accessExpiresAt.Unix()}
	nextRefresh[newRefreshToken] = jsonRefreshEntry{
		ClientID:  clientID,
		Scope:     e.Scope,
		ExpiresAt: refreshExpiresAt.Unix(),
	}
	if err := saveJSONState(s.filePath, jsonState{
		AccessTokens:  nextTokens,
		RefreshTokens: nextRefresh,
	}); err != nil {
		return "", false, err
	}
	s.tokens = nextTokens
	s.refreshTokens = nextRefresh
	return e.Scope, true, nil
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
	for k, e := range s.refreshTokens {
		if now >= e.ExpiresAt {
			delete(s.refreshTokens, k)
		}
	}
	return s.save()
}

func (s *jsonStore) Close() error {
	return nil
}
