package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const limitSchema = `
CREATE TABLE IF NOT EXISTS key_limits (
	name       TEXT PRIMARY KEY,
	limits     JSONB NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// defaultLimitsName is the reserved row for the fallback limits.
const defaultLimitsName = "default"

// LimitStore implements storage.LimitStore on PostgreSQL.
type LimitStore struct {
	pool *pgxpool.Pool
	def  limits.Limits // fallback when no default row exists yet
}

var _ storage.LimitStore = (*LimitStore)(nil)

// NewLimitStore ensures the schema and seeds the fallback default.
func NewLimitStore(ctx context.Context, pool *pgxpool.Pool, def limits.Limits) (*LimitStore, error) {
	if _, err := pool.Exec(ctx, limitSchema); err != nil {
		return nil, fmt.Errorf("init limits schema: %w", err)
	}
	return &LimitStore{pool: pool, def: def}, nil
}

func (s *LimitStore) get(ctx context.Context, name string) (limits.Limits, bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT limits FROM key_limits WHERE name = $1`, name).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return limits.Limits{}, false, nil
		}
		return limits.Limits{}, false, err
	}
	var l limits.Limits
	if err := json.Unmarshal(raw, &l); err != nil {
		return limits.Limits{}, false, fmt.Errorf("decode limits %s: %w", name, err)
	}
	return l, true, nil
}

func (s *LimitStore) set(ctx context.Context, name string, l limits.Limits) error {
	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO key_limits (name, limits, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (name) DO UPDATE SET limits = EXCLUDED.limits, updated_at = NOW()`,
		name, raw)
	return err
}

func (s *LimitStore) GetLimits(ctx context.Context, keyID string) (limits.Limits, bool, error) {
	if keyID == defaultLimitsName {
		return limits.Limits{}, false, nil
	}
	return s.get(ctx, keyID)
}

func (s *LimitStore) SetLimits(ctx context.Context, keyID string, l limits.Limits) error {
	return s.set(ctx, keyID, l)
}

func (s *LimitStore) DeleteLimits(ctx context.Context, keyID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM key_limits WHERE name = $1`, keyID)
	return err
}

func (s *LimitStore) GetDefaultLimits(ctx context.Context) (limits.Limits, error) {
	l, ok, err := s.get(ctx, defaultLimitsName)
	if err != nil {
		return limits.Limits{}, err
	}
	if !ok {
		return s.def, nil
	}
	return l, nil
}

func (s *LimitStore) SetDefaultLimits(ctx context.Context, l limits.Limits) error {
	return s.set(ctx, defaultLimitsName, l)
}

func (s *LimitStore) ListLimits(ctx context.Context) (map[string]limits.Limits, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, limits FROM key_limits WHERE name <> $1`, defaultLimitsName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]limits.Limits)
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		var l limits.Limits
		if err := json.Unmarshal(raw, &l); err != nil {
			return nil, fmt.Errorf("decode limits %s: %w", name, err)
		}
		out[name] = l
	}
	return out, rows.Err()
}
