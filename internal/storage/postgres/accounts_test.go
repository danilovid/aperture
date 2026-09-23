package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/danilovid/mutegate/internal/storage"
	"github.com/danilovid/mutegate/internal/storage/storagetest"
)

// The PostgreSQL store is tested against the real thing: schema, constraints
// and the single-use semantics that live in WHERE clauses cannot be proved
// against a fake. Without a database to point at, the suite skips.
func TestPostgresAccountStore(t *testing.T) {
	url := os.Getenv("MUTEGATE_TEST_DATABASE_URL")
	if url == "" {
		url = os.Getenv("APERTURE_TEST_DATABASE_URL") // its name before the rename
	}
	if url == "" {
		t.Skip("set MUTEGATE_TEST_DATABASE_URL to run the PostgreSQL account store tests")
	}
	storagetest.RunAccountStore(t, func(t *testing.T) storage.AccountStore {
		ctx := context.Background()
		pool, err := Open(ctx, url)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		t.Cleanup(pool.Close)
		store, err := NewAccountStore(ctx, pool)
		if err != nil {
			t.Fatalf("schema: %v", err)
		}
		return store
	})
}
