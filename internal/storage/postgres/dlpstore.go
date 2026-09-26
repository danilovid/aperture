package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mutegate/mutegate/internal/storage"
)

const dlpSchema = `
CREATE TABLE IF NOT EXISTS dlp_events (
	id            BIGSERIAL PRIMARY KEY,
	ts            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	key_id        TEXT NOT NULL DEFAULT '',
	model         TEXT NOT NULL DEFAULT '',
	provider      TEXT NOT NULL DEFAULT '',
	rule          TEXT NOT NULL DEFAULT '',
	"group"       TEXT NOT NULL DEFAULT '',
	action        TEXT NOT NULL DEFAULT '',
	masked_sample TEXT NOT NULL DEFAULT ''
);

-- Added separately so existing deployments upgrade in place.
ALTER TABLE dlp_events ADD COLUMN IF NOT EXISTS agent   TEXT NOT NULL DEFAULT '';
ALTER TABLE dlp_events ADD COLUMN IF NOT EXISTS session TEXT NOT NULL DEFAULT '';
ALTER TABLE dlp_events ADD COLUMN IF NOT EXISTS direction TEXT NOT NULL DEFAULT 'request';

CREATE INDEX IF NOT EXISTS idx_dlp_events_ts ON dlp_events(ts DESC);
CREATE INDEX IF NOT EXISTS idx_dlp_events_action ON dlp_events(action);
`

// DLPStore implements storage.DLPStore on PostgreSQL.
type DLPStore struct {
	pool *pgxpool.Pool
}

var _ storage.DLPStore = (*DLPStore)(nil)

// NewDLPStore ensures the schema exists.
func NewDLPStore(ctx context.Context, pool *pgxpool.Pool) (*DLPStore, error) {
	if err := ensureTenancy(ctx, pool); err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, dlpSchema); err != nil {
		return nil, fmt.Errorf("init dlp schema: %w", err)
	}
	// The feed is always read as "the last N in this organization", so the
	// index is the pair rather than the timestamp alone.
	if err := addOrgColumn(ctx, pool, "dlp_events",
		`CREATE INDEX IF NOT EXISTS idx_dlp_events_org_ts ON dlp_events(org_id, ts DESC)`); err != nil {
		return nil, err
	}
	return &DLPStore{pool: pool}, nil
}

// orgOf defaults a blank organization to the single-tenant one, so a row can
// never be written without an owner.
func orgOf(orgID string) string {
	if orgID == "" {
		return storage.DefaultOrgID
	}
	return orgID
}

// direction defaults a blank value so the column keeps its NOT NULL contract.
func direction(d string) string {
	if d == "" {
		return storage.DirectionRequest
	}
	return d
}

func (s *DLPStore) Insert(ctx context.Context, e storage.DLPEvent) error {
	ts := e.Ts
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO dlp_events (org_id, ts, key_id, model, provider, rule, "group", action, masked_sample, agent, session, direction)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		orgOf(e.OrgID), ts, e.KeyID, e.Model, e.Provider, e.Rule, e.Group, e.Action, e.MaskedSample,
		e.Agent, e.Session, direction(e.Direction))
	return err
}

func (s *DLPStore) List(ctx context.Context, orgID string, f storage.DLPFilter) ([]storage.DLPEvent, error) {
	// The organization is not one filter among many: it stays in the query
	// text below, where it cannot be left out by a code path that happens to
	// build no conditions. Everything here is appended to it.
	args := []any{orgOf(orgID)}
	var conds strings.Builder
	add := func(cond string, v any) {
		args = append(args, v)
		conds.WriteString(" AND " + fmt.Sprintf(cond, len(args)))
	}
	if f.Action != "" {
		add("action = $%d", f.Action)
	}
	if f.Rule != "" {
		add("rule = $%d", f.Rule)
	}
	if f.KeyID != "" {
		add("key_id = $%d", f.KeyID)
	}
	if f.Agent != "" {
		add("agent = $%d", f.Agent)
	}
	if f.Session != "" {
		add("session = $%d", f.Session)
	}
	if f.Direction != "" {
		add("direction = $%d", f.Direction)
	}
	if !f.Since.IsZero() {
		add("ts >= $%d", f.Since)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, org_id::text, ts, key_id, model, provider, rule, "group", action, masked_sample,
		       agent, session, direction
		FROM dlp_events
		WHERE org_id = $1::uuid%s
		ORDER BY ts DESC, id DESC LIMIT $%d`, conds.String(), len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storage.DLPEvent
	for rows.Next() {
		var e storage.DLPEvent
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Ts, &e.KeyID, &e.Model, &e.Provider, &e.Rule, &e.Group,
			&e.Action, &e.MaskedSample, &e.Agent, &e.Session, &e.Direction); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *DLPStore) Summary(ctx context.Context, orgID string, since time.Time) (storage.DLPSummary, error) {
	var sum storage.DLPSummary
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE action = 'blocked'),
		       COUNT(*) FILTER (WHERE action = 'redacted'),
		       COUNT(*) FILTER (WHERE action = 'alerted'),
		       COUNT(*) FILTER (WHERE action = 'suppressed')
		FROM dlp_events WHERE org_id = $1::uuid AND ts >= $2`, orgID, since,
	).Scan(&sum.Total, &sum.Blocked, &sum.Redacted, &sum.Alerted, &sum.Suppressed)
	return sum, err
}

// Aggregate groups events in the database so a report over a long period does
// not stream every row into the gateway.
func (s *DLPStore) Aggregate(ctx context.Context, orgID string, since time.Time) ([]storage.DLPBucket, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT rule, "group", key_id, agent, action,
		       COUNT(*), MIN(ts), MAX(ts), MIN(masked_sample)
		FROM dlp_events WHERE org_id = $1::uuid AND ts >= $2
		GROUP BY rule, "group", key_id, agent, action
		ORDER BY COUNT(*) DESC, rule
		LIMIT $3`, orgID, since, storage.MaxDLPBuckets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storage.DLPBucket
	for rows.Next() {
		var b storage.DLPBucket
		if err := rows.Scan(&b.Rule, &b.Group, &b.KeyID, &b.Agent, &b.Action,
			&b.Count, &b.First, &b.Last, &b.Sample); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
