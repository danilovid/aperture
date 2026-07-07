package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/danilovid/aperture/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
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
	if _, err := pool.Exec(ctx, dlpSchema); err != nil {
		return nil, fmt.Errorf("init dlp schema: %w", err)
	}
	return &DLPStore{pool: pool}, nil
}

func (s *DLPStore) Insert(ctx context.Context, e storage.DLPEvent) error {
	ts := e.Ts
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO dlp_events (ts, key_id, model, provider, rule, "group", action, masked_sample)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		ts, e.KeyID, e.Model, e.Provider, e.Rule, e.Group, e.Action, e.MaskedSample)
	return err
}

func (s *DLPStore) List(ctx context.Context, f storage.DLPFilter) ([]storage.DLPEvent, error) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
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
	if !f.Since.IsZero() {
		add("ts >= $%d", f.Since)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, ts, key_id, model, provider, rule, "group", action, masked_sample
		FROM dlp_events %s ORDER BY ts DESC, id DESC LIMIT $%d`, where, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []storage.DLPEvent
	for rows.Next() {
		var e storage.DLPEvent
		if err := rows.Scan(&e.ID, &e.Ts, &e.KeyID, &e.Model, &e.Provider, &e.Rule, &e.Group, &e.Action, &e.MaskedSample); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *DLPStore) Summary(ctx context.Context, since time.Time) (storage.DLPSummary, error) {
	var sum storage.DLPSummary
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE action = 'blocked'),
		       COUNT(*) FILTER (WHERE action = 'redacted'),
		       COUNT(*) FILTER (WHERE action = 'alerted')
		FROM dlp_events WHERE ts >= $1`, since,
	).Scan(&sum.Total, &sum.Blocked, &sum.Redacted, &sum.Alerted)
	return sum, err
}
