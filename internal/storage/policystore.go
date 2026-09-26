package storage

import (
	"context"
	"sync"

	"github.com/mutegate/mutegate/internal/inspector"
)

// PolicyStore persists DLP policies. Policies are looked up per mutegate-key
// ID with a fallback to the "default" policy.
type PolicyStore interface {
	// GetPolicy returns the policy bound to keyID; ok is false when none is set.
	GetPolicy(ctx context.Context, orgID, keyID string) (p inspector.Policy, ok bool, err error)
	// SetPolicy binds a policy to keyID.
	SetPolicy(ctx context.Context, orgID, keyID string, p inspector.Policy) error
	// DeletePolicy unbinds keyID so it falls back to the default policy.
	DeletePolicy(ctx context.Context, orgID, keyID string) error
	// GetDefaultPolicy returns the default policy.
	GetDefaultPolicy(ctx context.Context, orgID string) (inspector.Policy, error)
	// SetDefaultPolicy replaces the default policy.
	SetDefaultPolicy(ctx context.Context, orgID string, p inspector.Policy) error
	// ListPolicies returns all per-key policies (excluding the default).
	ListPolicies(ctx context.Context, orgID string) (map[string]inspector.Policy, error)
}

// MemPolicyStore is an in-memory PolicyStore (no-DB mode).
type MemPolicyStore struct {
	mu sync.RWMutex
	// seeded is the policy this gateway started with; it is what an
	// organization gets until it saves its own default.
	seeded inspector.Policy
	def    map[string]inspector.Policy            // orgID → default policy
	byKey  map[string]map[string]inspector.Policy // orgID → keyID → policy
}

// NewMemPolicyStore seeds the default policy (typically from env config).
func NewMemPolicyStore(def inspector.Policy) *MemPolicyStore {
	return &MemPolicyStore{
		seeded: def,
		def:    map[string]inspector.Policy{},
		byKey:  map[string]map[string]inspector.Policy{},
	}
}

var _ PolicyStore = (*MemPolicyStore)(nil)

func (s *MemPolicyStore) GetPolicy(_ context.Context, orgID, keyID string) (inspector.Policy, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byKey[orgID][keyID]
	return p, ok, nil
}

func (s *MemPolicyStore) SetPolicy(_ context.Context, orgID, keyID string, p inspector.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byKey[orgID] == nil {
		s.byKey[orgID] = map[string]inspector.Policy{}
	}
	s.byKey[orgID][keyID] = p
	return nil
}

func (s *MemPolicyStore) DeletePolicy(_ context.Context, orgID, keyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byKey[orgID], keyID)
	return nil
}

// GetDefaultPolicy falls back to the policy the gateway was started with, so
// a fresh organization is protected before anybody configures anything.
func (s *MemPolicyStore) GetDefaultPolicy(_ context.Context, orgID string) (inspector.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.def[orgID]; ok {
		return p, nil
	}
	return s.seeded, nil
}

func (s *MemPolicyStore) SetDefaultPolicy(_ context.Context, orgID string, p inspector.Policy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.def[orgID] = p
	return nil
}

func (s *MemPolicyStore) ListPolicies(_ context.Context, orgID string) (map[string]inspector.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]inspector.Policy, len(s.byKey[orgID]))
	for k, v := range s.byKey[orgID] {
		out[k] = v
	}
	return out, nil
}
