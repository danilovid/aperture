package storage_test

import (
	"testing"

	"github.com/mutegate/mutegate/internal/storage"
	"github.com/mutegate/mutegate/internal/storage/storagetest"
)

// The in-memory store backs tests and no-DB mode, so it is held to exactly
// the same contract as PostgreSQL.
func TestMemAccountStore(t *testing.T) {
	storagetest.RunAccountStore(t, func(t *testing.T) storage.AccountStore {
		return storage.NewMemAccountStore()
	})
}
