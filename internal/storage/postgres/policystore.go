package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const policySchema = `
CREATE TABLE IF NOT EXISTS dlp_policies (
	name       TEXT PRIMARY KEY,
	policy     JSONB NOT NULL,
	updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`

// defaultPolicyName is the reserved row for the fallback policy.
const defaultPolicyName = "default"

// PolicyStore implements storage.PolicyStore on PostgreSQL.
type PolicyStore struct {
	pool *pgxpool.Pool
	def  inspector.Policy // fallback when no default row exists yet
}

var _ storage.PolicyStore = (*PolicyStore)(nil)

// NewPolicyStore ensures the schema and seeds the fallback default policy.
func NewPolicyStore(ctx context.Context, pool *pgxpool.Pool, def inspector.Policy) (*PolicyStore, error) {
	if _, err := pool.Exec(ctx, policySchema); err != nil {
		return nil, fmt.Errorf("init policy schema: %w", err)
	}
	return &PolicyStore{pool: pool, def: def}, nil
}

func (s *PolicyStore) get(ctx context.Context, name string) (inspector.Policy, bool, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT policy FROM dlp_policies WHERE name = $1`, name).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return inspector.Policy{}, false, nil
		}
		return inspector.Policy{}, false, err
	}
	var p inspector.Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return inspector.Policy{}, false, fmt.Errorf("decode policy %s: %w", name, err)
	}
	return p, true, nil
}

func (s *PolicyStore) set(ctx context.Context, name string, p inspector.Policy) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO dlp_policies (name, policy, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (name) DO UPDATE SET policy = EXCLUDED.policy, updated_at = NOW()`,
		name, raw)
	return err
}

func (s *PolicyStore) GetPolicy(ctx context.Context, keyID string) (inspector.Policy, bool, error) {
	if keyID == defaultPolicyName {
		return inspector.Policy{}, false, nil
	}
	return s.get(ctx, keyID)
}

func (s *PolicyStore) SetPolicy(ctx context.Context, keyID string, p inspector.Policy) error {
	return s.set(ctx, keyID, p)
}

func (s *PolicyStore) DeletePolicy(ctx context.Context, keyID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM dlp_policies WHERE name = $1`, keyID)
	return err
}

func (s *PolicyStore) GetDefaultPolicy(ctx context.Context) (inspector.Policy, error) {
	p, ok, err := s.get(ctx, defaultPolicyName)
	if err != nil {
		return inspector.Policy{}, err
	}
	if !ok {
		return s.def, nil
	}
	return p, nil
}

func (s *PolicyStore) SetDefaultPolicy(ctx context.Context, p inspector.Policy) error {
	return s.set(ctx, defaultPolicyName, p)
}

func (s *PolicyStore) ListPolicies(ctx context.Context) (map[string]inspector.Policy, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, policy FROM dlp_policies WHERE name <> $1`, defaultPolicyName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]inspector.Policy)
	for rows.Next() {
		var name string
		var raw []byte
		if err := rows.Scan(&name, &raw); err != nil {
			return nil, err
		}
		var p inspector.Policy
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, fmt.Errorf("decode policy %s: %w", name, err)
		}
		out[name] = p
	}
	return out, rows.Err()
}
