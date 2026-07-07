package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/danilovid/aperture/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

const logSchema = `
CREATE TABLE IF NOT EXISTS request_logs (
	id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	ts                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	model             TEXT NOT NULL,
	provider          TEXT NOT NULL,
	prompt_tokens     INT NOT NULL DEFAULT 0,
	completion_tokens INT NOT NULL DEFAULT 0,
	total_tokens      INT NOT NULL DEFAULT 0,
	cost_usd          NUMERIC(12,8) NOT NULL DEFAULT 0,
	latency_ms        BIGINT NOT NULL DEFAULT 0,
	status_code       INT NOT NULL DEFAULT 0,
	key_id            UUID REFERENCES api_keys(id) ON DELETE SET NULL,
	error             TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_logs_ts    ON request_logs (ts DESC);
CREATE INDEX IF NOT EXISTS idx_request_logs_model ON request_logs (model);
`

// LogStore implements storage.LogStore for PostgreSQL.
type LogStore struct {
	pool *pgxpool.Pool
}

// NewLogStore creates a LogStore and ensures the schema exists.
func NewLogStore(ctx context.Context, pool *pgxpool.Pool) (*LogStore, error) {
	if _, err := pool.Exec(ctx, logSchema); err != nil {
		return nil, fmt.Errorf("init log schema: %w", err)
	}
	return &LogStore{pool: pool}, nil
}

func (s *LogStore) Insert(ctx context.Context, e storage.LogEntry) error {
	var keyID *string
	if e.KeyID != "" {
		keyID = &e.KeyID
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO request_logs
			(model, provider, prompt_tokens, completion_tokens, total_tokens,
			 cost_usd, latency_ms, status_code, key_id, error)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::uuid,$10)`,
		e.Model, e.Provider, e.PromptTokens, e.CompletionTokens, e.TotalTokens,
		e.CostUSD, e.LatencyMs, e.StatusCode, keyID, e.Error,
	)
	return err
}

func (s *LogStore) List(ctx context.Context, f storage.LogFilter) ([]storage.LogEntry, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, ts, model, provider,
		       prompt_tokens, completion_tokens, total_tokens,
		       cost_usd::float8, latency_ms, status_code,
		       COALESCE(key_id::text,''), error
		FROM request_logs
		ORDER BY ts DESC
		LIMIT $1 OFFSET $2`,
		f.Limit, f.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []storage.LogEntry
	for rows.Next() {
		var e storage.LogEntry
		if err := rows.Scan(
			&e.ID, &e.Ts, &e.Model, &e.Provider,
			&e.PromptTokens, &e.CompletionTokens, &e.TotalTokens,
			&e.CostUSD, &e.LatencyMs, &e.StatusCode,
			&e.KeyID, &e.Error,
		); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *LogStore) Summary(ctx context.Context, since time.Time) (storage.StatsSummary, error) {
	var sum storage.StatsSummary
	err := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cost_usd::float8), 0),
			COALESCE(AVG(latency_ms), 0),
			CASE WHEN COUNT(*) = 0 THEN 0
			     ELSE COUNT(*) FILTER (WHERE status_code >= 400)::float8 / COUNT(*)
			END
		FROM request_logs
		WHERE ts >= $1`, since,
	).Scan(
		&sum.Requests, &sum.PromptTokens, &sum.CompletionTokens,
		&sum.TotalTokens, &sum.CostUSD, &sum.AvgLatencyMs, &sum.ErrorRate,
	)
	return sum, err
}

func (s *LogStore) Timeseries(ctx context.Context, since time.Time, bucketHours int) ([]storage.TimeseriesBucket, error) {
	if bucketHours <= 0 {
		bucketHours = 1
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
			date_trunc('hour', ts) +
				((EXTRACT(HOUR FROM ts)::int / $2) * $2) * INTERVAL '1 hour' AS bucket,
			COUNT(*),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cost_usd::float8), 0),
			COALESCE(AVG(latency_ms), 0)
		FROM request_logs
		WHERE ts >= $1
		GROUP BY bucket
		ORDER BY bucket ASC`,
		since, bucketHours,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buckets []storage.TimeseriesBucket
	for rows.Next() {
		var b storage.TimeseriesBucket
		if err := rows.Scan(&b.Ts, &b.Requests, &b.TotalTokens, &b.CostUSD, &b.AvgLatencyMs); err != nil {
			return nil, err
		}
		buckets = append(buckets, b)
	}
	return buckets, rows.Err()
}

func (s *LogStore) ModelStats(ctx context.Context, since time.Time) ([]storage.ModelStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT
			model, provider,
			COUNT(*),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cost_usd::float8), 0),
			COALESCE(AVG(latency_ms), 0)
		FROM request_logs
		WHERE ts >= $1
		GROUP BY model, provider
		ORDER BY COUNT(*) DESC`,
		since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []storage.ModelStat
	for rows.Next() {
		var m storage.ModelStat
		if err := rows.Scan(&m.Model, &m.Provider, &m.Requests, &m.TotalTokens, &m.CostUSD, &m.AvgLatencyMs); err != nil {
			return nil, err
		}
		stats = append(stats, m)
	}
	return stats, rows.Err()
}
