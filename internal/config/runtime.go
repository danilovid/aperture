package config

import (
	"context"
	"strings"
	"sync"

	"github.com/danilovid/aperture/internal/storage"
)

// RuntimeStore holds provider API keys in memory (no-DB mode).
// Keys are set via POST /admin/config and stored only for the lifetime of the process.
type RuntimeStore struct {
	mu          sync.RWMutex
	apertureKey string
	name        string
	providers   map[string]string
}

const defaultRuntimeApertureKey = "dev"

func NewRuntimeStore(apertureKey string) *RuntimeStore {
	apertureKey = strings.TrimSpace(apertureKey)
	if apertureKey == "" {
		apertureKey = defaultRuntimeApertureKey
	}
	return &RuntimeStore{
		apertureKey: apertureKey,
		name:        "default",
		providers:   make(map[string]string),
	}
}

// KeyStore returns a storage.KeyStore backed by this runtime store.
func (r *RuntimeStore) KeyStore() storage.KeyStore {
	return &runtimeKeyStore{r}
}

type runtimeKeyStore struct {
	r *RuntimeStore
}

var _ storage.KeyStore = (*runtimeKeyStore)(nil)

func (s *runtimeKeyStore) GetByApertureKey(_ context.Context, apertureKey string) (*storage.Key, error) {
	s.r.mu.RLock()
	defer s.r.mu.RUnlock()
	if len(s.r.providers) == 0 || strings.TrimSpace(apertureKey) == "" || apertureKey != s.r.apertureKey {
		return nil, storage.ErrKeyNotFound
	}
	providers := make(map[string]string, len(s.r.providers))
	for k, v := range s.r.providers {
		providers[k] = v
	}
	return &storage.Key{
		ID:          "runtime",
		ApertureKey: s.r.apertureKey,
		Name:        s.r.name,
		Providers:   providers,
	}, nil
}

func (s *runtimeKeyStore) Create(_ context.Context, in storage.KeyCreateInput) (*storage.Key, error) {
	apertureKey := strings.TrimSpace(in.ApertureKey)
	if apertureKey == "" {
		return nil, storage.ErrInvalidInput
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "default"
	}

	s.r.mu.Lock()
	defer s.r.mu.Unlock()

	s.r.apertureKey = apertureKey
	s.r.name = name
	if s.r.providers == nil {
		s.r.providers = make(map[string]string)
	}
	for llm, key := range in.Providers {
		if key != "" {
			s.r.providers[llm] = key
		}
	}

	providers := make(map[string]string, len(s.r.providers))
	for k, v := range s.r.providers {
		providers[k] = v
	}
	return &storage.Key{
		ID:          "runtime",
		ApertureKey: s.r.apertureKey,
		Name:        s.r.name,
		Providers:   providers,
	}, nil
}

func (s *runtimeKeyStore) List(_ context.Context) ([]storage.Key, error) {
	s.r.mu.RLock()
	defer s.r.mu.RUnlock()
	if len(s.r.providers) == 0 {
		return []storage.Key{}, nil
	}
	return []storage.Key{
		{
			ID:          "runtime",
			ApertureKey: s.r.apertureKey,
			Name:        s.r.name,
		},
	}, nil
}

func (s *runtimeKeyStore) Delete(_ context.Context, id string) error {
	if id != "runtime" {
		return storage.ErrKeyNotFound
	}
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.providers = make(map[string]string)
	return nil
}

func (s *runtimeKeyStore) SetProviderKeys(_ context.Context, providers map[string]string) error {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	for llm, key := range providers {
		if key != "" {
			s.r.providers[llm] = key
		}
	}
	return nil
}

func (s *runtimeKeyStore) GetProviderKeys(_ context.Context) (map[string]string, error) {
	s.r.mu.RLock()
	defer s.r.mu.RUnlock()
	out := make(map[string]string, len(s.r.providers))
	for k, v := range s.r.providers {
		out[k] = v
	}
	return out, nil
}

func (s *runtimeKeyStore) ClearProviderKeys(_ context.Context) error {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	s.r.providers = make(map[string]string)
	return nil
}
