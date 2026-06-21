package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/K-RED90/gidm/internal/engine"
)

// GetSetting returns the value for key, or engine.ErrNotFound if unset.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, selectSetting, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sqlite: get setting %q: %w", key, engine.ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: get setting %q: %w", key, err)
	}
	return value, nil
}

// SetSetting upserts a key/value setting.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.ExecContext(ctx, upsertSetting, key, value); err != nil {
		return fmt.Errorf("sqlite: set setting %q: %w", key, err)
	}
	return nil
}

const selectSetting = `SELECT value FROM settings WHERE key = ?;`

const upsertSetting = `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value;`
