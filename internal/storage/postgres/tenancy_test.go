package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/storage"
	"github.com/danilovid/aperture/internal/storage/storagetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Isolation lives in WHERE clauses and primary keys, so it is proved against a
// real database. TestEveryQueryIsScopedByOrganization reads the SQL; this runs
// it. Without a database to point at, the suite skips.

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("APERTURE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set APERTURE_TEST_DATABASE_URL to run the PostgreSQL tenancy tests")
	}
	pool, err := Open(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedOrgs creates the two organizations the suite uses and clears whatever a
// previous run left behind, so each subtest starts from an empty pair.
func seedOrgs(t *testing.T, pool *pgxpool.Pool, tables ...string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{storagetest.OrgA, storagetest.OrgB} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO organizations (id, name, slug) VALUES ($1::uuid, $2, $2)
			ON CONFLICT (id) DO NOTHING`, id, "tenancy-"+id[:8]); err != nil {
			t.Fatalf("seed organization: %v", err)
		}
	}
	for _, table := range tables {
		if _, err := pool.Exec(ctx,
			`DELETE FROM `+table+` WHERE org_id = $1::uuid OR org_id = $2::uuid`,
			storagetest.OrgA, storagetest.OrgB); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}

func TestPostgresDLPStoreTenancy(t *testing.T) {
	storagetest.RunDLPStoreTenancy(t, func(t *testing.T) storage.DLPStore {
		pool := testPool(t)
		store, err := NewDLPStore(context.Background(), pool)
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		seedOrgs(t, pool, "dlp_events")
		return store
	})
}

func TestPostgresPolicyStoreTenancy(t *testing.T) {
	storagetest.RunPolicyStoreTenancy(t, func(t *testing.T) storage.PolicyStore {
		pool := testPool(t)
		store, err := NewPolicyStore(context.Background(), pool, inspector.Policy{Secrets: inspector.ActionAlert})
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		seedOrgs(t, pool, "dlp_policies")
		return store
	})
}

func TestPostgresLimitStoreTenancy(t *testing.T) {
	storagetest.RunLimitStoreTenancy(t, func(t *testing.T) storage.LimitStore {
		pool := testPool(t)
		store, err := NewLimitStore(context.Background(), pool, limits.Limits{})
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		seedOrgs(t, pool, "key_limits")
		return store
	})
}

// An installation that has been running since before multi-tenancy already has
// tables without an org_id and rows that belong to nobody. Upgrading must move
// them into the default organization rather than lose them or refuse to start,
// so the upgrade is done here against the real pre-tenancy schema.
func TestUpgradeFromSingleTenantSchema(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	// Start from the shape the tables had before: the base schema constants
	// are still the pre-tenancy definitions, org_id arrives by migration.
	for _, table := range []string{"dlp_events", "dlp_policies", "key_limits"} {
		if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
	for _, schema := range []string{dlpSchema, policySchema, limitSchema} {
		if _, err := pool.Exec(ctx, schema); err != nil {
			t.Fatalf("create the pre-tenancy schema: %v", err)
		}
	}

	// Rows written by the old binary: no organization, and a policy keyed by
	// name alone.
	if _, err := pool.Exec(ctx, `
		INSERT INTO dlp_events (key_id, model, provider, rule, "group", action, masked_sample)
		VALUES ('old-key', 'gpt-4o-mini', 'openai', 'email', 'pii', 'blocked', '***')`); err != nil {
		t.Fatalf("insert a pre-tenancy event: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO dlp_policies (name, policy) VALUES ('default', '{"secrets":"block"}')`); err != nil {
		t.Fatalf("insert a pre-tenancy policy: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO key_limits (name, limits) VALUES ('default', '{"budget_daily_usd":4}')`); err != nil {
		t.Fatalf("insert pre-tenancy limits: %v", err)
	}

	// Starting the new binary is what runs the migration.
	dlp, err := NewDLPStore(ctx, pool)
	if err != nil {
		t.Fatalf("upgrade the event store: %v", err)
	}
	policies, err := NewPolicyStore(ctx, pool, inspector.Policy{})
	if err != nil {
		t.Fatalf("upgrade the policy store: %v", err)
	}
	lims, err := NewLimitStore(ctx, pool, limits.Limits{})
	if err != nil {
		t.Fatalf("upgrade the limit store: %v", err)
	}

	events, err := dlp.List(ctx, storage.DefaultOrgID, storage.DLPFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Rule != "email" {
		t.Errorf("the pre-tenancy event did not land in the default organization: %+v", events)
	}

	p, err := policies.GetDefaultPolicy(ctx, storage.DefaultOrgID)
	if err != nil {
		t.Fatalf("get the default policy: %v", err)
	}
	if p.Secrets != inspector.ActionBlock {
		t.Errorf("the pre-tenancy default policy was lost: %+v", p)
	}

	l, err := lims.GetDefaultLimits(ctx, storage.DefaultOrgID)
	if err != nil {
		t.Fatalf("get the default limits: %v", err)
	}
	if l.BudgetDailyUSD != 4 {
		t.Errorf("the pre-tenancy default budget was lost: %+v", l)
	}

	// And the upgraded tables really are keyed by organization now: the same
	// name in a second organization is a second row, not a collision.
	seedOrgs(t, pool)
	if err := policies.SetDefaultPolicy(ctx, storagetest.OrgB, inspector.Policy{Secrets: inspector.ActionAlert}); err != nil {
		t.Fatalf("save a policy for a second organization: %v", err)
	}
	again, err := policies.GetDefaultPolicy(ctx, storage.DefaultOrgID)
	if err != nil {
		t.Fatalf("get the default policy: %v", err)
	}
	if again.Secrets != inspector.ActionBlock {
		t.Error("a second organization's default policy overwrote the upgraded one")
	}
}
