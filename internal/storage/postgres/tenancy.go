package postgres

import (
	"context"
	"fmt"

	"github.com/danilovid/mutegate/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Every table that holds traffic belongs to an organization. This file owns
// the part of the schema that makes that true, and the migration that brings
// an installation created before multi-tenancy into line.

// orgSchema creates the organizations table and the row that existing data
// belongs to. It runs before any store's own schema, because every one of
// them references it.
const orgSchema = `
CREATE TABLE IF NOT EXISTS organizations (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	name       TEXT NOT NULL,
	slug       TEXT UNIQUE NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	deleted_at TIMESTAMPTZ
);

-- The organization a single-tenant installation works in, and the one every
-- row created before this migration is moved into. Fixed id so the backfill
-- is the same on every installation.
INSERT INTO organizations (id, name, slug)
VALUES ('` + storage.DefaultOrgID + `', 'Default', 'default')
ON CONFLICT (id) DO NOTHING;
`

// ensureTenancy creates the organizations table and the default row. Safe to
// call from every store's constructor: all of it is idempotent.
func ensureTenancy(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, orgSchema); err != nil {
		return fmt.Errorf("init organizations: %w", err)
	}
	return nil
}

// repointPrimaryKey moves a table keyed by name alone onto (org_id, name).
// Two organizations may both have a policy called "default", and before this
// the second one would collide with the first.
func repointPrimaryKey(ctx context.Context, pool *pgxpool.Pool, table string) error {
	stmt := fmt.Sprintf(`
DO $$
BEGIN
	IF EXISTS (
		SELECT 1 FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		WHERE t.relname = '%s' AND c.contype = 'p'
		  AND array_length(c.conkey, 1) = 1
	) THEN
		EXECUTE 'ALTER TABLE %s DROP CONSTRAINT ' ||
			(SELECT conname FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid
			 WHERE t.relname = '%s' AND c.contype = 'p');
		ALTER TABLE %s ADD PRIMARY KEY (org_id, name);
	END IF;
END $$;`, table, table, table, table)
	if _, err := pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("repoint the primary key of %s: %w", table, err)
	}
	return nil
}

// addOrgColumn gives a table its org_id in three steps, in this order so that
// a running older binary keeps working in between: add the column nullable,
// move existing rows into the default organization, then require it.
func addOrgColumn(ctx context.Context, pool *pgxpool.Pool, table string, index string) error {
	stmts := []string{
		fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS org_id UUID REFERENCES organizations(id) ON DELETE CASCADE`, table),
		fmt.Sprintf(`UPDATE %s SET org_id = '%s' WHERE org_id IS NULL`, table, storage.DefaultOrgID),
		fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN org_id SET NOT NULL`, table),
		fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN org_id SET DEFAULT '%s'`, table, storage.DefaultOrgID),
		index,
	}
	for _, stmt := range stmts {
		if stmt == "" {
			continue
		}
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("scope %s by organization: %w", table, err)
		}
	}
	return nil
}
