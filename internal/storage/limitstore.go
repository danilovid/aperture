package storage

import (
	"context"
	"sync"

	"github.com/danilovid/aperture/internal/limits"
)

// LimitStore persists per-key budgets and rate limits, with a default that
// applies to keys without their own entry. It mirrors PolicyStore so the two
// behave the same way for callers and operators.
type LimitStore interface {
	GetLimits(ctx context.Context, orgID, keyID string) (l limits.Limits, ok bool, err error)
	SetLimits(ctx context.Context, orgID, keyID string, l limits.Limits) error
	DeleteLimits(ctx context.Context, orgID, keyID string) error
	GetDefaultLimits(ctx context.Context, orgID string) (limits.Limits, error)
	SetDefaultLimits(ctx context.Context, orgID string, l limits.Limits) error
	ListLimits(ctx context.Context, orgID string) (map[string]limits.Limits, error)
}

// MemLimitStore is an in-memory LimitStore (no-DB mode).
type MemLimitStore struct {
	mu sync.RWMutex
	// seeded is what the gateway was started with; every organization gets it
	// until it sets its own ceiling.
	seeded limits.Limits
	def    map[string]limits.Limits            // orgID → default
	byKey  map[string]map[string]limits.Limits // orgID → keyID → limits
}

// NewMemLimitStore seeds the default limits (typically from env config).
func NewMemLimitStore(def limits.Limits) *MemLimitStore {
	return &MemLimitStore{
		seeded: def,
		def:    map[string]limits.Limits{},
		byKey:  map[string]map[string]limits.Limits{},
	}
}

var _ LimitStore = (*MemLimitStore)(nil)

func (s *MemLimitStore) GetLimits(_ context.Context, orgID, keyID string) (limits.Limits, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.byKey[orgID][keyID]
	return l, ok, nil
}

func (s *MemLimitStore) SetLimits(_ context.Context, orgID, keyID string, l limits.Limits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byKey[orgID] == nil {
		s.byKey[orgID] = map[string]limits.Limits{}
	}
	s.byKey[orgID][keyID] = l
	return nil
}

func (s *MemLimitStore) DeleteLimits(_ context.Context, orgID, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey[orgID], keyID)
	return nil
}

func (s *MemLimitStore) GetDefaultLimits(_ context.Context, orgID string) (limits.Limits, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l, ok := s.def[orgID]; ok {
		return l, nil
	}
	return s.seeded, nil
}

func (s *MemLimitStore) SetDefaultLimits(_ context.Context, orgID string, l limits.Limits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.def[orgID] = l
	return nil
}

func (s *MemLimitStore) ListLimits(_ context.Context, orgID string) (map[string]limits.Limits, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]limits.Limits, len(s.byKey[orgID]))
	for k, v := range s.byKey[orgID] {
		out[k] = v
	}
	return out, nil
}
