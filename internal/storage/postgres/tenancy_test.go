package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/secrets"
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

// Closing an organization has to stop its agents, not just its console. The
// check lives in the key lookup, so this is where it can be proved.
func TestClosedOrganizationStopsItsKeys(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	accounts, err := NewAccountStore(ctx, pool)
	if err != nil {
		t.Fatalf("accounts schema: %v", err)
	}
	keys, err := NewKeyStore(ctx, pool, nil)
	if err != nil {
		t.Fatalf("keys schema: %v", err)
	}
	// Unique per run: these rows outlive the test, since the point of a soft
	// delete is that nothing is removed.
	unique := time.Now().Format("150405.000000000")
	org, err := accounts.CreateOrganization(ctx, "Closing", "closing-"+unique)
	if err != nil {
		t.Fatal(err)
	}

	token := "ap-closing-" + unique
	if _, err := keys.Create(ctx, org.ID, token, "agent", map[string]string{"openai": "sk-x"}); err != nil {
		t.Fatalf("create key: %v", err)
	}
	if _, err := keys.GetByApertureKey(ctx, token); err != nil {
		t.Fatalf("the key does not work to begin with: %v", err)
	}

	if err := accounts.DeleteOrganization(ctx, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.GetByApertureKey(ctx, token); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Errorf("a closed organization's key still resolves: %v", err)
	}

	if err := accounts.RestoreOrganization(ctx, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := keys.GetByApertureKey(ctx, token); err != nil {
		t.Errorf("restoring did not bring the key back: %v", err)
	}
}

func newTestProviderStore(t *testing.T, cipher *secrets.Cipher) (*ProviderStore, *pgxpool.Pool) {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	if _, err := NewKeyStore(ctx, pool, cipher); err != nil {
		t.Fatalf("keys schema: %v", err)
	}
	store, err := NewProviderStore(ctx, pool, cipher)
	if err != nil {
		t.Fatalf("providers schema: %v", err)
	}
	seedOrgs(t, pool, "providers")
	return store, pool
}

func TestPostgresProviderStore(t *testing.T) {
	storagetest.RunProviderStore(t, func(t *testing.T) storage.ProviderStore {
		s, _ := newTestProviderStore(t, nil)
		return s
	})
}

// A proxy address carries a password as often as not, and a provider key is a
// provider key: with an encryption key configured, neither is readable in the
// table.
func TestProviderSecretsAreEncryptedAtRest(t *testing.T) {
	cipher, err := secrets.NewCipher(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	s, pool := newTestProviderStore(t, cipher)
	ctx := context.Background()
	if err := s.PutProvider(ctx, storagetest.OrgA, storage.ProviderConfig{
		Name: "openai", Kind: storage.KindOpenAI, APIKey: "sk-plaintext-key",
		ProxyURL: "http://user:hunter2@proxy.corp:3128", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	var key, proxy string
	if err := pool.QueryRow(ctx, `SELECT api_key, proxy_url FROM providers WHERE org_id = $1::uuid AND name = 'openai'`,
		storagetest.OrgA).Scan(&key, &proxy); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(key, "sk-plaintext") || strings.Contains(proxy, "hunter2") {
		t.Fatalf("secrets stored readable: key %q proxy %q", key, proxy)
	}
	got, err := s.GetProvider(ctx, storagetest.OrgA, "openai")
	if err != nil || got.APIKey != "sk-plaintext-key" || got.ProxyURL != "http://user:hunter2@proxy.corp:3128" {
		t.Errorf("secrets did not come back: %+v %v", got, err)
	}
}

// The keys saved on the old Settings screen become the organization's
// providers once, and only once: a provider deleted afterwards stays deleted.
func TestOldSettingsKeysAreAdoptedOnce(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	keys, err := NewKeyStore(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	seedOrgs(t, pool)
	pool.Exec(ctx, `DELETE FROM providers WHERE org_id = $1::uuid`, storagetest.OrgA)
	if err := keys.SetProviderKeys(ctx, storagetest.OrgA, map[string]string{"openai": "sk-from-settings", "mystery": "x"}); err != nil {
		t.Fatal(err)
	}

	store, err := NewProviderStore(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetProvider(ctx, storagetest.OrgA, "openai")
	if err != nil || got.APIKey != "sk-from-settings" || got.Kind != storage.KindOpenAI || !got.Enabled {
		t.Fatalf("the Settings key was not adopted: %+v %v", got, err)
	}
	if _, err := store.GetProvider(ctx, storagetest.OrgA, "mystery"); err == nil {
		t.Error("a key for no known provider was turned into a provider")
	}
	if left, _ := keys.GetProviderKeys(ctx, storagetest.OrgA); left["openai"] != "" {
		t.Error("the adopted key was left behind, so it would be adopted again")
	}

	// Deleted by the organization, it must not come back on the next start.
	store.DeleteProvider(ctx, storagetest.OrgA, "openai")
	again, err := NewProviderStore(ctx, pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.GetProvider(ctx, storagetest.OrgA, "openai"); err == nil {
		t.Error("a deleted provider came back after a restart")
	}
}

// A webhook address is the credential to post to it; with an encryption key
// configured it is unreadable in the table, and each organization has its own.
func TestAlertSettingsAreEncryptedAndPerOrganization(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cipher, err := secrets.NewCipher(strings.Repeat("cd", 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewAlertStore(ctx, pool, cipher)
	if err != nil {
		t.Fatal(err)
	}
	seedOrgs(t, pool, "alert_settings")

	if _, ok, _ := store.GetAlertSettings(ctx, storagetest.OrgA); ok {
		t.Fatal("an organization with no settings has some")
	}
	secret := `{"url":"https://hooks.slack.com/services/T/B/org-a-secret"}`
	if err := store.SetAlertSettings(ctx, storagetest.OrgA, []byte(secret)); err != nil {
		t.Fatal(err)
	}
	var raw string
	pool.QueryRow(ctx, `SELECT settings FROM alert_settings WHERE org_id = $1::uuid`, storagetest.OrgA).Scan(&raw)
	if strings.Contains(raw, "org-a-secret") {
		t.Fatalf("the webhook is readable in the table: %s", raw)
	}
	got, ok, err := store.GetAlertSettings(ctx, storagetest.OrgA)
	if err != nil || !ok || string(got) != secret {
		t.Errorf("settings came back as %q %v %v", got, ok, err)
	}
	if _, ok, _ := store.GetAlertSettings(ctx, storagetest.OrgB); ok {
		t.Error("B sees A's alert settings")
	}
}

func newTestAuditStore(t *testing.T) (*AuditStore, *pgxpool.Pool, string) {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	accounts, err := NewAccountStore(ctx, pool)
	if err != nil {
		t.Fatalf("accounts schema: %v", err)
	}
	store, err := NewAuditStore(ctx, pool)
	if err != nil {
		t.Fatalf("audit schema: %v", err)
	}
	seedOrgs(t, pool, "audit_log")
	email := fmt.Sprintf("audit-%d@acme.test", time.Now().UnixNano())
	user, err := accounts.CreateUser(ctx, email, "Ann", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return store, pool, user.ID
}

func TestPostgresAuditStore(t *testing.T) {
	storagetest.RunAuditStore(t, func(t *testing.T) (storage.AuditStore, string) {
		s, _, userID := newTestAuditStore(t)
		return s, userID
	})
}

// A journal that forgets who did something once they are gone is useless for
// the one question it is kept to answer about people who have left.
func TestAuditEntriesOutliveTheirActor(t *testing.T) {
	s, pool, userID := newTestAuditStore(t)
	ctx := context.Background()
	if err := s.Record(ctx, storage.AuditEntry{
		OrgID: storagetest.OrgA, ActorKind: storage.ActorUser, ActorID: userID,
		ActorLabel: "ann@acme.test", Action: "key.delete", Target: "prod",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	got, err := s.List(ctx, storagetest.OrgA, storage.AuditFilter{})
	if err != nil || len(got) != 1 {
		t.Fatalf("entry went with its actor: %v, %+v", err, got)
	}
	if got[0].ActorLabel != "ann@acme.test" || got[0].ActorID != userID {
		t.Errorf("the entry no longer says who: %+v", got[0])
	}
}
