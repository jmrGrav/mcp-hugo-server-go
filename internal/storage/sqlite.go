package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteStore struct {
	db *sql.DB
}

func NewSQLite(path string) (Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS access_tokens (
			token TEXT PRIMARY KEY,
			scope TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS refresh_tokens (
			token TEXT PRIMARY KEY,
			client_id TEXT NOT NULL,
			scope TEXT NOT NULL,
			expires_at INTEGER NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create refresh_tokens schema: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS oauth_clients (
			client_id TEXT PRIMARY KEY,
			secret_hash TEXT NOT NULL DEFAULT '',
			redirect_uris TEXT NOT NULL,
			scopes TEXT NOT NULL,
			effective_scope TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("create oauth_clients schema: %w", err)
	}

	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) UpsertOAuthClient(clientID, secretHash string, enabled bool, redirectURIs, scopes []string) error {
	redirectData, err := json.Marshal(redirectURIs)
	if err != nil {
		return fmt.Errorf("marshal redirect_uris: %w", err)
	}
	scopeData, err := json.Marshal(scopes)
	if err != nil {
		return fmt.Errorf("marshal scopes: %w", err)
	}
	effectiveScope := ""
	if len(scopes) > 0 {
		effectiveScope = scopes[len(scopes)-1]
	}
	if effectiveScope == "" {
		effectiveScope = "content.read"
	}
	_, err = s.db.Exec(
		`INSERT OR REPLACE INTO oauth_clients (client_id, secret_hash, redirect_uris, scopes, effective_scope, enabled, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		clientID, secretHash, string(redirectData), string(scopeData), effectiveScope, boolToInt(enabled), time.Now().Unix(),
	)
	return err
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *sqliteStore) AddAccessToken(token, scope string, expiresAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO access_tokens (token, scope, expires_at) VALUES (?, ?, ?)`,
		token, scope, expiresAt.Unix(),
	)
	return err
}

func (s *sqliteStore) ValidateAccessToken(token string) (string, bool) {
	var scope string
	err := s.db.QueryRow(
		`SELECT scope FROM access_tokens WHERE token = ? AND expires_at > ?`,
		token, time.Now().Unix(),
	).Scan(&scope)
	if err != nil {
		return "", false
	}
	return scope, true
}

func (s *sqliteStore) AddRefreshToken(token, clientID, scope string, expiresAt time.Time) error {
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO refresh_tokens (token, client_id, scope, expires_at) VALUES (?, ?, ?, ?)`,
		token, clientID, scope, expiresAt.Unix(),
	)
	return err
}

func (s *sqliteStore) ValidateRefreshToken(token, clientID string) (string, bool) {
	var scope string
	err := s.db.QueryRow(
		`SELECT scope FROM refresh_tokens WHERE token = ? AND client_id = ? AND expires_at > ?`,
		token, clientID, time.Now().Unix(),
	).Scan(&scope)
	if err != nil {
		return "", false
	}
	return scope, true
}

func (s *sqliteStore) ExchangeRefreshToken(oldToken, clientID, newRefreshToken, newAccessToken string, accessExpiresAt, refreshExpiresAt time.Time) (string, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()

	var scope string
	err = tx.QueryRow(
		`SELECT scope FROM refresh_tokens WHERE token = ? AND client_id = ? AND expires_at > ?`,
		oldToken, clientID, time.Now().Unix(),
	).Scan(&scope)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(`DELETE FROM refresh_tokens WHERE token = ?`, oldToken); err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO access_tokens (token, scope, expires_at) VALUES (?, ?, ?)`,
		newAccessToken, scope, accessExpiresAt.Unix(),
	); err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO refresh_tokens (token, client_id, scope, expires_at) VALUES (?, ?, ?, ?)`,
		newRefreshToken, clientID, scope, refreshExpiresAt.Unix(),
	); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return scope, true, nil
}

func (s *sqliteStore) PurgeExpiredTokens() error {
	_, err := s.db.Exec(`DELETE FROM access_tokens WHERE expires_at <= ?`, time.Now().Unix())
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM refresh_tokens WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}
