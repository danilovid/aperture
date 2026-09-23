package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/danilovid/mutegate/internal/secrets"
	"github.com/danilovid/mutegate/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const alertSchema = `
CREATE TABLE IF NOT EXISTS alert_settings (
	org_id     UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
	-- Sealed with MUTEGATE_ENCRYPTION_KEY when it is set: the webhook address
	-- in here is the credential to post to it.
	settings   TEXT NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// AlertStore implements storage.AlertStore on PostgreSQL.
type AlertStore struct {
	pool   *pgxpool.Pool
	cipher *secrets.Cipher
}

var _ storage.AlertStore = (*AlertStore)(nil)

func NewAlertStore(ctx context.Context, pool *pgxpool.Pool, cipher *secrets.Cipher) (*AlertStore, error) {
	if err := ensureTenancy(ctx, pool); err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, alertSchema); err != nil {
		return nil, fmt.Errorf("init alert settings schema: %w", err)
	}
	return &AlertStore{pool: pool, cipher: cipher}, nil
}

func (s *AlertStore) GetAlertSettings(ctx context.Context, orgID string) ([]byte, bool, error) {
	var stored string
	err := s.pool.QueryRow(ctx,
		`SELECT settings FROM alert_settings WHERE org_id = $1::uuid`, orgOf(orgID)).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if s.cipher != nil {
		plain, err := s.cipher.Decrypt(stored)
		if err != nil {
			return nil, false, err
		}
		return []byte(plain), true, nil
	}
	if secrets.IsEncrypted(stored) {
		return nil, false, fmt.Errorf("alert settings are encrypted but MUTEGATE_ENCRYPTION_KEY is not set")
	}
	return []byte(stored), true, nil
}

func (s *AlertStore) SetAlertSettings(ctx context.Context, orgID string, raw []byte) error {
	stored := string(raw)
	if s.cipher != nil {
		var err error
		if stored, err = s.cipher.Encrypt(stored); err != nil {
			return err
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO alert_settings (org_id, settings, updated_at) VALUES ($1::uuid, $2, NOW())
		ON CONFLICT (org_id) DO UPDATE SET settings = EXCLUDED.settings, updated_at = NOW()`,
		orgOf(orgID), stored)
	return err
}
