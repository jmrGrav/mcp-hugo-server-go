package storage

import (
	"sync"
	"time"
)

type Store interface {
	AddAccessToken(token, scope string, expiresAt time.Time) error
	ValidateAccessToken(token string) (scope string, ok bool)
	AddRefreshToken(token, clientID, scope string, expiresAt time.Time) error
	ValidateRefreshToken(token, clientID string) (scope string, ok bool)
	ExchangeRefreshToken(oldToken, clientID, newRefreshToken, newAccessToken string, accessExpiresAt, refreshExpiresAt time.Time) (scope string, ok bool, err error)
	PurgeExpiredTokens() error
	Close() error
}

type memoryEntry struct {
	scope     string
	expiresAt time.Time
}

type memoryRefreshEntry struct {
	clientID  string
	scope     string
	expiresAt time.Time
}

type memoryStore struct {
	mu            sync.RWMutex
	tokens        map[string]memoryEntry
	refreshTokens map[string]memoryRefreshEntry
}

func NewMemory() Store {
	return &memoryStore{
		tokens:        make(map[string]memoryEntry),
		refreshTokens: make(map[string]memoryRefreshEntry),
	}
}

func (m *memoryStore) AddAccessToken(token, scope string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token] = memoryEntry{scope: scope, expiresAt: expiresAt}
	return nil
}

func (m *memoryStore) ValidateAccessToken(token string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.tokens[token]
	if !ok || !time.Now().Before(e.expiresAt) {
		return "", false
	}
	return e.scope, true
}

func (m *memoryStore) ValidateAccessTokenDetails(token string) (string, time.Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.tokens[token]
	if !ok || !time.Now().Before(e.expiresAt) {
		return "", time.Time{}, false
	}
	return e.scope, e.expiresAt, true
}

func (m *memoryStore) AddRefreshToken(token, clientID, scope string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshTokens[token] = memoryRefreshEntry{
		clientID:  clientID,
		scope:     scope,
		expiresAt: expiresAt,
	}
	return nil
}

func (m *memoryStore) ValidateRefreshToken(token, clientID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.refreshTokens[token]
	if !ok || e.clientID != clientID || !time.Now().Before(e.expiresAt) {
		return "", false
	}
	return e.scope, true
}

func (m *memoryStore) ExchangeRefreshToken(oldToken, clientID, newRefreshToken, newAccessToken string, accessExpiresAt, refreshExpiresAt time.Time) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.refreshTokens[oldToken]
	if !ok || e.clientID != clientID || !time.Now().Before(e.expiresAt) {
		return "", false, nil
	}
	delete(m.refreshTokens, oldToken)
	m.tokens[newAccessToken] = memoryEntry{scope: e.scope, expiresAt: accessExpiresAt}
	m.refreshTokens[newRefreshToken] = memoryRefreshEntry{
		clientID:  clientID,
		scope:     e.scope,
		expiresAt: refreshExpiresAt,
	}
	return e.scope, true, nil
}

func (m *memoryStore) PurgeExpiredTokens() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for k, e := range m.tokens {
		if !now.Before(e.expiresAt) {
			delete(m.tokens, k)
		}
	}
	for k, e := range m.refreshTokens {
		if !now.Before(e.expiresAt) {
			delete(m.refreshTokens, k)
		}
	}
	return nil
}

func (m *memoryStore) Close() error {
	return nil
}
