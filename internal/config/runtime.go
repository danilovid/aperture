package config

import (
	"context"
	"crypto/subtle"
	"sync"

	"github.com/danilovid/aperture/internal/storage"
)

// RuntimeStore holds provider API keys in memory (no-DB mode).
// Keys are set via POST /admin/config and stored only for the lifetime of the process.
type RuntimeStore struct {
	mu          sync.RWMutex
	apertureKey string
	providers   map[string]string
}

// NewRuntimeStore creates an in-memory store that accepts only the given
// aperture key as a Bearer token.
func NewRuntimeStore(apertureKey string) *RuntimeStore {
	return &RuntimeStore{apertureKey: apertureKey, providers: make(map[string]string)}
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
	if subtle.ConstantTimeCompare([]byte(apertureKey), []byte(s.r.apertureKey)) != 1 {
		return nil, storage.ErrKeyNotFound
	}
	if len(s.r.providers) == 0 {
		return nil, storage.ErrKeyNotFound
	}
	providers := make(map[string]string, len(s.r.providers))
	for k, v := range s.r.providers {
		providers[k] = v
	}
	return &storage.Key{
		ID:          "runtime",
		ApertureKey: apertureKey,
		Name:        "default",
		Providers:   providers,
	}, nil
}

func (s *runtimeKeyStore) Create(_ context.Context, _, _ string, _ map[string]string) (*storage.Key, error) {
	return nil, storage.ErrNotSupported
}

func (s *runtimeKeyStore) List(_ context.Context) ([]storage.Key, error) {
	return nil, nil
}

func (s *runtimeKeyStore) Delete(_ context.Context, _ string) error {
	return storage.ErrNotSupported
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
