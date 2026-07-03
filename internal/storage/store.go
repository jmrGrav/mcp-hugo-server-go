package storage

import (
	"sync"
	"time"
)

type Store interface {
	AddAccessToken(token, scope string, expiresAt time.Time) error
	ValidateAccessToken(token string) (scope string, ok bool)
	PurgeExpiredTokens() error
	Close() error
}

type memoryEntry struct {
	scope     string
	expiresAt time.Time
}

type memoryStore struct {
	mu     sync.RWMutex
	tokens map[string]memoryEntry
}

func NewMemory() Store {
	return &memoryStore{tokens: make(map[string]memoryEntry)}
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
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.scope, true
}

func (m *memoryStore) PurgeExpiredTokens() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for k, e := range m.tokens {
		if now.After(e.expiresAt) {
			delete(m.tokens, k)
		}
	}
	return nil
}

func (m *memoryStore) Close() error {
	return nil
}
