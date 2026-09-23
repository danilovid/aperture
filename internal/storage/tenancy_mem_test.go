package storage_test

import (
	"testing"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/storage"
	"github.com/danilovid/aperture/internal/storage/storagetest"
)

// The in-memory stores back no-DB mode and most of the test suite, so they are
// held to the same isolation contract as PostgreSQL.

func TestMemDLPStoreTenancy(t *testing.T) {
	storagetest.RunDLPStoreTenancy(t, func(t *testing.T) storage.DLPStore {
		return storage.NewMemDLPStore(100)
	})
}

func TestMemPolicyStoreTenancy(t *testing.T) {
	storagetest.RunPolicyStoreTenancy(t, func(t *testing.T) storage.PolicyStore {
		return storage.NewMemPolicyStore(inspector.Policy{Secrets: inspector.ActionAlert})
	})
}

func TestMemLimitStoreTenancy(t *testing.T) {
	storagetest.RunLimitStoreTenancy(t, func(t *testing.T) storage.LimitStore {
		return storage.NewMemLimitStore(limits.Limits{})
	})
}

func TestMemProviderStore(t *testing.T) {
	storagetest.RunProviderStore(t, func(t *testing.T) storage.ProviderStore {
		return storage.NewMemProviderStore()
	})
}
