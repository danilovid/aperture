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

const providerSchema = `
CREATE TABLE IF NOT EXISTS providers (
	org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	name       TEXT NOT NULL,
	kind       TEXT NOT NULL,
	base_url   TEXT NOT NULL DEFAULT '',
	-- Both sealed with APERTURE_ENCRYPTION_KEY when it is set. A proxy
	-- address is a secret as often as not: it carries a login and password.
	api_key    TEXT NOT NULL DEFAULT '',
	proxy_url  TEXT NOT NULL DEFAULT '',
	prefixes   TEXT[] NOT NULL DEFAULT '{}',
	timeout_ms INTEGER NOT NULL DEFAULT 0,
	enabled    BOOLEAN NOT NULL DEFAULT TRUE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (org_id, name)
);
`

// adoptSettingsKeys moves the keys organizations saved on the old Settings
// screen onto their providers. Those keys lived on a per-organization row of
// api_keys that no traffic had read since the shared "dev" key was retired;
// here they become what they were always meant to be — the organization's
// default for each provider. The rows they came from are removed in the same
// transaction, so this happens once and a provider deleted later does not
// come back on the next restart.
//
// The keys are copied as stored. Both tables seal with the same cipher, so a
// sealed value is valid in either.
const adoptSettingsKeys = `
INSERT INTO providers (org_id, name, kind, api_key)
SELECT a.org_id, pk.llm, pk.llm, pk.key
FROM provider_keys pk JOIN api_keys a ON a.id = pk.api_key_id
WHERE a.key_hash LIKE 'config:%' AND pk.key <> ''
  AND pk.llm IN ('openai', 'anthropic', 'groq', 'jev')
ON CONFLICT (org_id, name) DO NOTHING`

const forgetSettingsKeys = `
DELETE FROM provider_keys
WHERE llm IN ('openai', 'anthropic', 'groq', 'jev')
  AND api_key_id IN (SELECT id FROM api_keys WHERE key_hash LIKE 'config:%')`

// ProviderStore implements storage.ProviderStore on PostgreSQL.
type ProviderStore struct {
	pool   *pgxpool.Pool
	cipher *secrets.Cipher // nil → secrets stored as given
}

var _ storage.ProviderStore = (*ProviderStore)(nil)

// NewProviderStore ensures the schema and adopts any keys from the old
// Settings row. It needs the key store's schema to exist first.
func NewProviderStore(ctx context.Context, pool *pgxpool.Pool, cipher *secrets.Cipher) (*ProviderStore, error) {
	if err := ensureTenancy(ctx, pool); err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, providerSchema); err != nil {
		return nil, fmt.Errorf("init providers schema: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, adoptSettingsKeys); err != nil {
		return nil, fmt.Errorf("adopt settings keys: %w", err)
	}
	if _, err := tx.Exec(ctx, forgetSettingsKeys); err != nil {
		return nil, fmt.Errorf("adopt settings keys: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ProviderStore{pool: pool, cipher: cipher}, nil
}

func (s *ProviderStore) seal(v string) (string, error) {
	if s.cipher == nil || v == "" {
		return v, nil
	}
	return s.cipher.Encrypt(v)
}

func (s *ProviderStore) open(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if s.cipher == nil {
		if secrets.IsEncrypted(v) {
			return "", fmt.Errorf("a provider secret is encrypted but APERTURE_ENCRYPTION_KEY is not set")
		}
		return v, nil
	}
	return s.cipher.Decrypt(v)
}

const providerColumns = `name, kind, base_url, api_key, proxy_url, prefixes, timeout_ms, enabled, updated_at`

func (s *ProviderStore) scan(row pgx.Row) (*storage.ProviderConfig, error) {
	var p storage.ProviderConfig
	var kind string
	if err := row.Scan(&p.Name, &kind, &p.BaseURL, &p.APIKey, &p.ProxyURL, &p.Prefixes,
		&p.TimeoutMS, &p.Enabled, &p.UpdatedAt); err != nil {
		return nil, err
	}
	p.Kind = storage.ProviderKind(kind)
	var err error
	if p.APIKey, err = s.open(p.APIKey); err != nil {
		return nil, err
	}
	if p.ProxyURL, err = s.open(p.ProxyURL); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *ProviderStore) ListProviders(ctx context.Context, orgID string) ([]storage.ProviderConfig, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+providerColumns+` FROM providers
		WHERE org_id = $1::uuid ORDER BY name`, orgOf(orgID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.ProviderConfig{}
	for rows.Next() {
		p, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *ProviderStore) GetProvider(ctx context.Context, orgID, name string) (*storage.ProviderConfig, error) {
	p, err := s.scan(s.pool.QueryRow(ctx, `
		SELECT `+providerColumns+` FROM providers
		WHERE org_id = $1::uuid AND name = $2`, orgOf(orgID), name))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrProviderNotFound
	}
	return p, err
}

func (s *ProviderStore) PutProvider(ctx context.Context, orgID string, p storage.ProviderConfig) error {
	key, err := s.seal(p.APIKey)
	if err != nil {
		return err
	}
	proxy, err := s.seal(p.ProxyURL)
	if err != nil {
		return err
	}
	prefixes := p.Prefixes
	if prefixes == nil {
		prefixes = []string{}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO providers (org_id, name, kind, base_url, api_key, proxy_url, prefixes, timeout_ms, enabled, updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (org_id, name) DO UPDATE SET
			kind = EXCLUDED.kind, base_url = EXCLUDED.base_url, api_key = EXCLUDED.api_key,
			proxy_url = EXCLUDED.proxy_url, prefixes = EXCLUDED.prefixes,
			timeout_ms = EXCLUDED.timeout_ms, enabled = EXCLUDED.enabled, updated_at = NOW()`,
		orgOf(orgID), p.Name, string(p.Kind), p.BaseURL, key, proxy, prefixes, p.TimeoutMS, p.Enabled)
	return err
}

func (s *ProviderStore) DeleteProvider(ctx context.Context, orgID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM providers WHERE org_id = $1::uuid AND name = $2`, orgOf(orgID), name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrProviderNotFound
	}
	return nil
}
