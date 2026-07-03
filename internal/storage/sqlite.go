package storage

import (
	"database/sql"
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

	return &sqliteStore{db: db}, nil
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

func (s *sqliteStore) PurgeExpiredTokens() error {
	_, err := s.db.Exec(`DELETE FROM access_tokens WHERE expires_at <= ?`, time.Now().Unix())
	return err
}

func (s *sqliteStore) Close() error {
	return s.db.Close()
}
