package storage

import (
	"context"
	"sync"

	"github.com/danilovid/aperture/internal/inspector"
)

// PolicyStore persists DLP policies. Policies are looked up per aperture-key
// ID with a fallback to the "default" policy.
type PolicyStore interface {
	// GetPolicy returns the policy bound to keyID; ok is false when none is set.
	GetPolicy(ctx context.Context, keyID string) (p inspector.Policy, ok bool, err error)
	// SetPolicy binds a policy to keyID.
	SetPolicy(ctx context.Context, keyID string, p inspector.Policy) error
	// DeletePolicy unbinds keyID so it falls back to the default policy.
	DeletePolicy(ctx context.Context, keyID string) error
	// GetDefaultPolicy returns the default policy.
	GetDefaultPolicy(ctx context.Context) (inspector.Policy, error)
	// SetDefaultPolicy replaces the default policy.
	SetDefaultPolicy(ctx context.Context, p inspector.Policy) error
	// ListPolicies returns all per-key policies (excluding the default).
	ListPolicies(ctx context.Context) (map[string]inspector.Policy, error)
}

// MemPolicyStore is an in-memory PolicyStore (no-DB mode).
type MemPolicyStore struct {
	mu    sync.RWMutex
	def   inspector.Policy
	byKey map[string]inspector.Policy
}

// NewMemPolicyStore seeds the default policy (typically from env config).
func NewMemPolicyStore(def inspector.Policy) *MemPolicyStore {
	return &MemPolicyStore{def: def, byKey: make(map[string]inspector.Policy)}
}

var _ PolicyStore = (*MemPolicyStore)(nil)

func (s *MemPolicyStore) GetPolicy(_ context.Context, keyID string) (inspector.Policy, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byKey[keyID]
	return p, ok, nil
}

func (s *MemPolicyStore) SetPolicy(_ context.Context, keyID string, p inspector.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[keyID] = p
	return nil
}

func (s *MemPolicyStore) DeletePolicy(_ context.Context, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey, keyID)
	return nil
}

func (s *MemPolicyStore) GetDefaultPolicy(_ context.Context) (inspector.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.def, nil
}

func (s *MemPolicyStore) SetDefaultPolicy(_ context.Context, p inspector.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.def = p
	return nil
}

func (s *MemPolicyStore) ListPolicies(_ context.Context) (map[string]inspector.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]inspector.Policy, len(s.byKey))
	for k, v := range s.byKey {
		out[k] = v
	}
	return out, nil
}
