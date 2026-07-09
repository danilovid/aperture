package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/danilovid/aperture/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE TABLE IF NOT EXISTS api_keys (
	id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	aperture_key TEXT UNIQUE NOT NULL,
	name         TEXT NOT NULL DEFAULT '',
	created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS provider_keys (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
	llm        TEXT NOT NULL,
	key        TEXT NOT NULL,
	UNIQUE(api_key_id, llm)
);

CREATE INDEX IF NOT EXISTS idx_api_keys_aperture_key ON api_keys(aperture_key);
CREATE INDEX IF NOT EXISTS idx_provider_keys_api_key_id ON provider_keys(api_key_id);
`

// KeyStore implements storage.KeyStore for PostgreSQL.
type KeyStore struct {
	pool               *pgxpool.Pool
	defaultApertureKey string
}

var _ storage.KeyStore = (*KeyStore)(nil)

// NewKeyStore creates a KeyStore and ensures the schema exists.
func NewKeyStore(ctx context.Context, pool *pgxpool.Pool, defaultApertureKey string) (*KeyStore, error) {
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &KeyStore{
		pool:               pool,
		defaultApertureKey: normalizeDefaultApertureKey(defaultApertureKey),
	}, nil
}

// GetByApertureKey returns the key and all its provider keys.
func (s *KeyStore) GetByApertureKey(ctx context.Context, apertureKey string) (*storage.Key, error) {
	var k storage.Key
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, aperture_key, name, created_at::text FROM api_keys WHERE aperture_key = $1`,
		apertureKey,
	).Scan(&k.ID, &k.ApertureKey, &k.Name, &k.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrKeyNotFound
		}
		return nil, err
	}
	providers, err := s.loadProviders(ctx, k.ID)
	if err != nil {
		return nil, err
	}
	k.Providers = providers
	return &k, nil
}

func (s *KeyStore) Create(ctx context.Context, in storage.KeyCreateInput) (*storage.Key, error) {
	apertureKey := strings.TrimSpace(in.ApertureKey)
	if apertureKey == "" {
		return nil, storage.ErrInvalidInput
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "default"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var created storage.Key
	err = tx.QueryRow(ctx,
		`INSERT INTO api_keys (aperture_key, name)
		 VALUES ($1, $2)
		 RETURNING id::text, aperture_key, name, created_at::text`,
		apertureKey, name,
	).Scan(&created.ID, &created.ApertureKey, &created.Name, &created.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, storage.ErrKeyExists
		}
		return nil, err
	}

	created.Providers = make(map[string]string)
	for llm, key := range in.Providers {
		if key == "" {
			continue
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO provider_keys (api_key_id, llm, key)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (api_key_id, llm) DO UPDATE SET key = EXCLUDED.key`,
			created.ID, llm, key,
		)
		if err != nil {
			return nil, fmt.Errorf("upsert provider key %s: %w", llm, err)
		}
		created.Providers[llm] = key
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &created, nil
}

func (s *KeyStore) loadProviders(ctx context.Context, apiKeyID string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT llm, key FROM provider_keys WHERE api_key_id = $1::uuid`, apiKeyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	providers := make(map[string]string)
	for rows.Next() {
		var llm, key string
		if err := rows.Scan(&llm, &key); err != nil {
			return nil, err
		}
		providers[llm] = key
	}
	return providers, rows.Err()
}

// List returns all aperture keys without provider key values.
func (s *KeyStore) List(ctx context.Context) ([]storage.Key, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, aperture_key, name, created_at::text FROM api_keys ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []storage.Key
	for rows.Next() {
		var k storage.Key
		if err := rows.Scan(&k.ID, &k.ApertureKey, &k.Name, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Delete removes an aperture key and all its provider keys (cascade).
func (s *KeyStore) Delete(ctx context.Context, id string) error {
	r, err := s.pool.Exec(ctx, `DELETE FROM api_keys WHERE id = $1::uuid`, id)
	if err != nil {
		return err
	}
	if r.RowsAffected() == 0 {
		return storage.ErrKeyNotFound
	}
	return nil
}

// SetProviderKeys upserts the configured default aperture key and sets provider keys.
// Only non-empty values are written; existing keys for other providers are preserved.
func (s *KeyStore) SetProviderKeys(ctx context.Context, providers map[string]string) error {
	// Upsert the default aperture key.
	var apiKeyID string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_keys (aperture_key, name)
		VALUES ($1, 'default')
		ON CONFLICT (aperture_key) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`,
		normalizeDefaultApertureKey(s.defaultApertureKey),
	).Scan(&apiKeyID)
	if err != nil {
		return fmt.Errorf("upsert api_key: %w", err)
	}
	// Upsert each non-empty provider key.
	for llm, key := range providers {
		if key == "" {
			continue
		}
		_, err := s.pool.Exec(ctx, `
			INSERT INTO provider_keys (api_key_id, llm, key)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (api_key_id, llm) DO UPDATE SET key = EXCLUDED.key`,
			apiKeyID, llm, key,
		)
		if err != nil {
			return fmt.Errorf("upsert provider key %s: %w", llm, err)
		}
	}
	return nil
}

// GetProviderKeys returns all provider keys for the default aperture key.
func (s *KeyStore) GetProviderKeys(ctx context.Context) (map[string]string, error) {
	key, err := s.GetByApertureKey(ctx, normalizeDefaultApertureKey(s.defaultApertureKey))
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return key.Providers, nil
}

// ClearProviderKeys removes all provider keys for the default aperture key.
func (s *KeyStore) ClearProviderKeys(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM provider_keys
		WHERE api_key_id = (SELECT id FROM api_keys WHERE aperture_key = $1)`,
		normalizeDefaultApertureKey(s.defaultApertureKey),
	)
	return err
}

func normalizeDefaultApertureKey(apertureKey string) string {
	apertureKey = strings.TrimSpace(apertureKey)
	if apertureKey == "" {
		return "dev"
	}
	return apertureKey
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
