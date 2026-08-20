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
	GetLimits(ctx context.Context, keyID string) (l limits.Limits, ok bool, err error)
	SetLimits(ctx context.Context, keyID string, l limits.Limits) error
	DeleteLimits(ctx context.Context, keyID string) error
	GetDefaultLimits(ctx context.Context) (limits.Limits, error)
	SetDefaultLimits(ctx context.Context, l limits.Limits) error
	ListLimits(ctx context.Context) (map[string]limits.Limits, error)
}

// MemLimitStore is an in-memory LimitStore (no-DB mode).
type MemLimitStore struct {
	mu    sync.RWMutex
	def   limits.Limits
	byKey map[string]limits.Limits
}

// NewMemLimitStore seeds the default limits (typically from env config).
func NewMemLimitStore(def limits.Limits) *MemLimitStore {
	return &MemLimitStore{def: def, byKey: make(map[string]limits.Limits)}
}

var _ LimitStore = (*MemLimitStore)(nil)

func (s *MemLimitStore) GetLimits(_ context.Context, keyID string) (limits.Limits, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.byKey[keyID]
	return l, ok, nil
}

func (s *MemLimitStore) SetLimits(_ context.Context, keyID string, l limits.Limits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[keyID] = l
	return nil
}

func (s *MemLimitStore) DeleteLimits(_ context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey, keyID)
	return nil
}

func (s *MemLimitStore) GetDefaultLimits(context.Context) (limits.Limits, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.def, nil
}

func (s *MemLimitStore) SetDefaultLimits(_ context.Context, l limits.Limits) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.def = l
	return nil
}

func (s *MemLimitStore) ListLimits(context.Context) (map[string]limits.Limits, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]limits.Limits, len(s.byKey))
	for k, v := range s.byKey {
		out[k] = v
	}
	return out, nil
}
