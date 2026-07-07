package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/danilovid/aperture/internal/secrets"
	"github.com/danilovid/aperture/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Aperture keys are stored as sha256 hashes (key_hash) with a masked display
// form (key_hint); the raw token never touches the database. Provider keys
// are encrypted with AES-GCM when a Cipher is configured.
const schema = `
CREATE TABLE IF NOT EXISTS api_keys (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	key_hash   TEXT UNIQUE NOT NULL,
	key_hint   TEXT NOT NULL DEFAULT '',
	name       TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS provider_keys (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	api_key_id UUID NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
	llm        TEXT NOT NULL,
	key        TEXT NOT NULL,
	UNIQUE(api_key_id, llm)
);

CREATE INDEX IF NOT EXISTS idx_provider_keys_api_key_id ON provider_keys(api_key_id);
`

// migrate upgrades tables created before key hashing existed: the plaintext
// aperture_key column is hashed into key_hash and then dropped.
const migrate = `
DO $$
BEGIN
	IF EXISTS (SELECT 1 FROM information_schema.columns
	           WHERE table_name = 'api_keys' AND column_name = 'aperture_key') THEN
		ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_hash TEXT;
		ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_hint TEXT NOT NULL DEFAULT '';
		UPDATE api_keys SET
			key_hash = encode(sha256(convert_to(aperture_key, 'UTF8')), 'hex'),
			key_hint = CASE WHEN length(aperture_key) > 14
				THEN left(aperture_key, 7) || '************' || right(aperture_key, 4)
				ELSE repeat('*', length(aperture_key)) END
		WHERE key_hash IS NULL;
		ALTER TABLE api_keys ALTER COLUMN key_hash SET NOT NULL;
		ALTER TABLE api_keys ADD CONSTRAINT api_keys_key_hash_unique UNIQUE (key_hash);
		ALTER TABLE api_keys DROP COLUMN aperture_key;
	END IF;
END $$;
`

// KeyStore implements storage.KeyStore for PostgreSQL.
type KeyStore struct {
	pool   *pgxpool.Pool
	cipher *secrets.Cipher // nil → provider keys stored in plaintext
}

var _ storage.KeyStore = (*KeyStore)(nil)

// NewKeyStore ensures the schema exists and migrates pre-hash tables.
// cipher may be nil; then provider keys are stored unencrypted.
func NewKeyStore(ctx context.Context, pool *pgxpool.Pool, cipher *secrets.Cipher) (*KeyStore, error) {
	if _, err := pool.Exec(ctx, migrate); err != nil {
		return nil, fmt.Errorf("migrate schema: %w", err)
	}
	if _, err := pool.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &KeyStore{pool: pool, cipher: cipher}, nil
}

func (s *KeyStore) sealProviderKey(key string) (string, error) {
	if s.cipher == nil {
		return key, nil
	}
	return s.cipher.Encrypt(key)
}

func (s *KeyStore) openProviderKey(stored string) (string, error) {
	if s.cipher == nil {
		if secrets.IsEncrypted(stored) {
			return "", fmt.Errorf("provider key is encrypted but APERTURE_ENCRYPTION_KEY is not set")
		}
		return stored, nil
	}
	return s.cipher.Decrypt(stored)
}

// GetByApertureKey looks the token up by hash and returns decrypted provider keys.
func (s *KeyStore) GetByApertureKey(ctx context.Context, apertureKey string) (*storage.Key, error) {
	var k storage.Key
	err := s.pool.QueryRow(ctx,
		`SELECT id::text, key_hint, name, created_at::text FROM api_keys WHERE key_hash = $1`,
		secrets.HashToken(apertureKey),
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
		var llm, stored string
		if err := rows.Scan(&llm, &stored); err != nil {
			return nil, err
		}
		key, err := s.openProviderKey(stored)
		if err != nil {
			return nil, fmt.Errorf("provider key %s: %w", llm, err)
		}
		providers[llm] = key
	}
	return providers, rows.Err()
}

// Create inserts a new aperture key (hashed) with its provider keys (encrypted).
func (s *KeyStore) Create(ctx context.Context, apertureKey, name string, providers map[string]string) (*storage.Key, error) {
	var k storage.Key
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_keys (key_hash, key_hint, name)
		VALUES ($1, $2, $3)
		RETURNING id::text, name, created_at::text`,
		secrets.HashToken(apertureKey), secrets.Hint(apertureKey), name,
	).Scan(&k.ID, &k.Name, &k.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert api_key: %w", err)
	}
	// Return the raw key once — this is the caller's only chance to see it.
	k.ApertureKey = apertureKey
	k.Providers = make(map[string]string, len(providers))
	for llm, key := range providers {
		if key == "" {
			continue
		}
		sealed, err := s.sealProviderKey(key)
		if err != nil {
			return nil, fmt.Errorf("encrypt provider key %s: %w", llm, err)
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO provider_keys (api_key_id, llm, key)
			VALUES ($1::uuid, $2, $3)`,
			k.ID, llm, sealed,
		); err != nil {
			return nil, fmt.Errorf("insert provider key %s: %w", llm, err)
		}
		k.Providers[llm] = key
	}
	return &k, nil
}

// List returns all aperture keys; ApertureKey carries the masked hint only.
func (s *KeyStore) List(ctx context.Context) ([]storage.Key, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id::text, key_hint, name, created_at::text FROM api_keys ORDER BY created_at DESC`,
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

// SetProviderKeys upserts the default "dev" aperture key and sets provider keys.
// Only non-empty values are written; existing keys for other providers are preserved.
func (s *KeyStore) SetProviderKeys(ctx context.Context, providers map[string]string) error {
	var apiKeyID string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO api_keys (key_hash, key_hint, name)
		VALUES ($1, $2, 'default')
		ON CONFLICT (key_hash) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text`,
		secrets.HashToken("dev"), secrets.Hint("dev"),
	).Scan(&apiKeyID)
	if err != nil {
		return fmt.Errorf("upsert api_key: %w", err)
	}
	for llm, key := range providers {
		if key == "" {
			continue
		}
		sealed, err := s.sealProviderKey(key)
		if err != nil {
			return fmt.Errorf("encrypt provider key %s: %w", llm, err)
		}
		_, err = s.pool.Exec(ctx, `
			INSERT INTO provider_keys (api_key_id, llm, key)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (api_key_id, llm) DO UPDATE SET key = EXCLUDED.key`,
			apiKeyID, llm, sealed,
		)
		if err != nil {
			return fmt.Errorf("upsert provider key %s: %w", llm, err)
		}
	}
	return nil
}

// GetProviderKeys returns all provider keys for the default "dev" aperture key.
func (s *KeyStore) GetProviderKeys(ctx context.Context) (map[string]string, error) {
	key, err := s.GetByApertureKey(ctx, "dev")
	if err != nil {
		if errors.Is(err, storage.ErrKeyNotFound) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return key.Providers, nil
}

// ClearProviderKeys removes all provider keys for the default "dev" aperture key.
func (s *KeyStore) ClearProviderKeys(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM provider_keys
		WHERE api_key_id = (SELECT id FROM api_keys WHERE key_hash = $1)`,
		secrets.HashToken("dev"),
	)
	return err
}
