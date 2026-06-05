package store

import (
	"context"
	"encoding/json"
	"fmt"
)

// GetSetting unmarshals the JSON value of a setting key into dest.
func (s *Store) GetSetting(ctx context.Context, key string, dest any) error {
	raw, err := s.GetSettingRaw(ctx, key)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("decode setting %s: %w", key, err)
	}
	return nil
}

// GetSettingRaw returns the raw JSON value of a setting key.
func (s *Store) GetSettingRaw(ctx context.Context, key string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := s.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw); err != nil {
		return nil, fmt.Errorf("get setting %s: %w", key, err)
	}
	return raw, nil
}

// SetSetting upserts a setting, JSON-encoding the value.
func (s *Store) SetSetting(ctx context.Context, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode setting %s: %w", key, err)
	}
	const q = `
		INSERT INTO settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
	if _, err := s.pool.Exec(ctx, q, key, encoded); err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// AllSettings returns every setting as raw JSON, keyed by name.
func (s *Store) AllSettings(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	out := make(map[string]json.RawMessage)
	for rows.Next() {
		var key string
		var val json.RawMessage
		if err := rows.Scan(&key, &val); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out[key] = val
	}
	return out, rows.Err()
}
