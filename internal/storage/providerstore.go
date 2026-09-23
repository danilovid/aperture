package storage

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ProviderKind is which API an upstream speaks.
type ProviderKind string

const (
	KindOpenAI    ProviderKind = "openai"
	KindAnthropic ProviderKind = "anthropic"
	KindGroq      ProviderKind = "groq"
	KindJev       ProviderKind = "jev"
	// KindCompatible is anything that speaks the OpenAI API at its own
	// address: DeepSeek, Qwen, a vLLM or Ollama inside the network.
	KindCompatible ProviderKind = "openai-compatible"
)

// BuiltinKinds are the providers the gateway knows by name. Each exists at
// most once per organization and is named after its kind, which is also the
// name an aperture key's own provider keys are filed under.
var BuiltinKinds = []ProviderKind{KindOpenAI, KindAnthropic, KindGroq, KindJev}

// Builtin reports whether the kind is one of the named built-ins.
func (k ProviderKind) Builtin() bool {
	for _, b := range BuiltinKinds {
		if k == b {
			return true
		}
	}
	return false
}

// ValidKind reports whether s is a kind this build knows.
func ValidKind(s string) bool {
	k := ProviderKind(s)
	return k.Builtin() || k == KindCompatible
}

// ProviderConfig is one upstream as an organization has set it up: where it
// is, how to authenticate to it and which way the traffic goes out.
//
// APIKey and ProxyURL are secrets — a proxy address routinely carries a
// username and password — so they are never serialised, and a store keeps
// them encrypted when it has a key to do it with.
type ProviderConfig struct {
	Name     string       `json:"name"`
	Kind     ProviderKind `json:"kind"`
	BaseURL  string       `json:"base_url,omitempty"`
	APIKey   string       `json:"-"`
	ProxyURL string       `json:"-"`
	// Prefixes route models to an OpenAI-compatible provider: a model whose
	// name starts with one of them goes here. Built-ins route by their own
	// rules and ignore this.
	Prefixes []string `json:"prefixes,omitempty"`
	// TimeoutMS bounds the wait for the first byte of an answer. Zero means
	// the gateway's default.
	TimeoutMS int       `json:"timeout_ms,omitempty"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ErrProviderNotFound is a provider the organization has not configured.
var ErrProviderNotFound = errors.New("provider not found")

// ProviderStore holds each organization's upstreams.
type ProviderStore interface {
	ListProviders(ctx context.Context, orgID string) ([]ProviderConfig, error)
	GetProvider(ctx context.Context, orgID, name string) (*ProviderConfig, error)
	// PutProvider creates or replaces the provider called p.Name.
	PutProvider(ctx context.Context, orgID string, p ProviderConfig) error
	DeleteProvider(ctx context.Context, orgID, name string) error
}

// MemProviderStore keeps providers in memory, for tests.
type MemProviderStore struct {
	mu    sync.RWMutex
	byOrg map[string]map[string]ProviderConfig
}

var _ ProviderStore = (*MemProviderStore)(nil)

func NewMemProviderStore() *MemProviderStore {
	return &MemProviderStore{byOrg: map[string]map[string]ProviderConfig{}}
}

func copyProvider(p ProviderConfig) ProviderConfig {
	p.Prefixes = append([]string(nil), p.Prefixes...)
	return p
}

func (s *MemProviderStore) ListProviders(_ context.Context, orgID string) ([]ProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ProviderConfig{}
	for _, p := range s.byOrg[orgID] {
		out = append(out, copyProvider(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *MemProviderStore) GetProvider(_ context.Context, orgID, name string) (*ProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byOrg[orgID][name]
	if !ok {
		return nil, ErrProviderNotFound
	}
	c := copyProvider(p)
	return &c, nil
}

func (s *MemProviderStore) PutProvider(_ context.Context, orgID string, p ProviderConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byOrg[orgID] == nil {
		s.byOrg[orgID] = map[string]ProviderConfig{}
	}
	p.UpdatedAt = time.Now()
	s.byOrg[orgID][p.Name] = copyProvider(p)
	return nil
}

func (s *MemProviderStore) DeleteProvider(_ context.Context, orgID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byOrg[orgID][name]; !ok {
		return ErrProviderNotFound
	}
	delete(s.byOrg[orgID], name)
	return nil
}
