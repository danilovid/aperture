package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/danilovid/mutegate/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The audit_log table itself is part of the accounts schema; it was laid down
// with the people it describes. What it lacked was a way to name an actor who
// is not a person — a service token, the operator — and a name that survives
// the person being deleted.
const auditColumns = `
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS actor_kind  TEXT NOT NULL DEFAULT 'user';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS actor_id    TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS actor_label TEXT NOT NULL DEFAULT '';
`

// AuditStore implements storage.AuditStore on PostgreSQL.
type AuditStore struct {
	pool *pgxpool.Pool
}

var _ storage.AuditStore = (*AuditStore)(nil)

// NewAuditStore needs the accounts schema, which creates the table.
func NewAuditStore(ctx context.Context, pool *pgxpool.Pool) (*AuditStore, error) {
	if _, err := pool.Exec(ctx, auditColumns); err != nil {
		return nil, fmt.Errorf("init audit log: %w", err)
	}
	return &AuditStore{pool: pool}, nil
}

func (s *AuditStore) Record(ctx context.Context, e storage.AuditEntry) error {
	meta, err := json.Marshal(e.Meta)
	if err != nil {
		return err
	}
	if e.Meta == nil {
		meta = []byte("{}")
	}
	// actor_user_id keeps the link to a person while they exist; the label
	// keeps the entry readable after.
	var userID *string
	if e.ActorKind == storage.ActorUser && e.ActorID != "" {
		userID = &e.ActorID
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_log (org_id, actor_user_id, actor_kind, actor_id, actor_label, action, target, meta, ip)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)`,
		orgOf(e.OrgID), userID, e.ActorKind, e.ActorID, e.ActorLabel, e.Action, e.Target, meta, e.IP)
	return err
}

func (s *AuditStore) List(ctx context.Context, orgID string, f storage.AuditFilter) ([]storage.AuditEntry, error) {
	args := []any{orgOf(orgID)}
	conds := ""
	if f.Group != "" {
		// Compared, not matched: the group comes from a query string, and as
		// a LIKE pattern "%" would be every group and "p_licy" would be one.
		args = append(args, f.Group)
		conds += fmt.Sprintf(" AND split_part(action, '.', 1) = $%d", len(args))
	}
	if f.Before > 0 {
		args = append(args, f.Before)
		conds += fmt.Sprintf(" AND id < $%d", len(args))
	}
	args = append(args, storage.AuditLimit(f.Limit))
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, created_at, actor_kind, actor_id, actor_label, action, target, meta, ip
		FROM audit_log
		WHERE org_id = $1::uuid%s
		ORDER BY id DESC LIMIT $%d`, conds, len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.AuditEntry{}
	for rows.Next() {
		var e storage.AuditEntry
		var meta []byte
		if err := rows.Scan(&e.ID, &e.Time, &e.ActorKind, &e.ActorID, &e.ActorLabel,
			&e.Action, &e.Target, &meta, &e.IP); err != nil {
			return nil, err
		}
		if len(meta) > 0 && string(meta) != "{}" {
			if err := json.Unmarshal(meta, &e.Meta); err != nil {
				return nil, err
			}
		}
		e.OrgID = orgID
		out = append(out, e)
	}
	return out, rows.Err()
}
