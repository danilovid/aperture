package storage_test

import (
	"testing"

	"github.com/mutegate/mutegate/internal/inspector"
	"github.com/mutegate/mutegate/internal/limits"
	"github.com/mutegate/mutegate/internal/storage"
	"github.com/mutegate/mutegate/internal/storage/storagetest"
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

func TestMemAuditStore(t *testing.T) {
	storagetest.RunAuditStore(t, func(t *testing.T) (storage.AuditStore, string) {
		return storage.NewMemAuditStore(), "33333333-3333-3333-3333-333333333333"
	})
}
