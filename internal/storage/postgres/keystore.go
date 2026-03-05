package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/danilovid/aperture/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE TABLE IF NOT EXISTS api_keys (
	id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	aperture_key TEXT UNIQUE NOT NULL,
	openai_api_key TEXT NOT NULL,
	name TEXT DEFAULT '',
	created_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_api_keys_aperture_key ON api_keys(aperture_key);
`

// KeyStore implements storage.KeyStore for PostgreSQL.
type KeyStore struct {
	pool *pgxpool.Pool
}

// NewKeyStore creates a KeyStore and ensures the schema exists.
func NewKeyStore(ctx context.Context, connString string) (*KeyStore, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &KeyStore{pool: pool}, nil
}

// GetByApertureKey implements storage.KeyStore.
func (s *KeyStore) GetByApertureKey(ctx context.Context, apertureKey string) (*storage.Key, error) {
	var k storage.Key
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, aperture_key, openai_api_key, name, created_at::text
		 FROM api_keys WHERE aperture_key = $1`,
		apertureKey,
	).Scan(&k.ID, &k.ApertureKey, &k.OpenAIAPIKey, &k.Name, &k.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ErrKeyNotFound
		}
		return nil, err
	}
	return &k, nil
}

// Create implements storage.KeyStore.
func (s *KeyStore) Create(ctx context.Context, apertureKey, openaiAPIKey, name string) (*storage.Key, error) {
	id := uuid.New().String()
	var createdAt string
	err := s.pool.QueryRow(ctx,
		`INSERT INTO api_keys (id, aperture_key, openai_api_key, name)
		 VALUES ($1::uuid, $2, $3, $4)
		 RETURNING created_at::text`,
		id, apertureKey, openaiAPIKey, name,
	).Scan(&createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	return &storage.Key{
		ID:          id,
		ApertureKey: apertureKey,
		Name:        name,
		CreatedAt:   createdAt,
	}, nil
}

// List implements storage.KeyStore.
func (s *KeyStore) List(ctx context.Context) ([]storage.Key, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, aperture_key, '' as openai_api_key, name, created_at::text
		 FROM api_keys ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []storage.Key
	for rows.Next() {
		var k storage.Key
		if err := rows.Scan(&k.ID, &k.ApertureKey, &k.OpenAIAPIKey, &k.Name, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Delete implements storage.KeyStore.
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
