package config

import (
	"context"
	"sync"

	"github.com/danilovid/aperture/internal/storage"
)

// RuntimeStore holds OpenAI key in memory. For test/simple setups.
// Admin can set it via POST /admin/config.
type RuntimeStore struct {
	mu  sync.RWMutex
	key string
}

// NewRuntimeStore creates a store, optionally seeded from env.
func NewRuntimeStore(seedKey string) *RuntimeStore {
	r := &RuntimeStore{}
	if seedKey != "" {
		r.key = seedKey
	}
	return r
}

// SetOpenAIKey sets the OpenAI API key.
func (r *RuntimeStore) SetOpenAIKey(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.key = key
}

// ClearKey removes the stored key.
func (r *RuntimeStore) ClearKey() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.key = ""
}

// GetOpenAIKey returns the current key (or empty).
func (r *RuntimeStore) GetOpenAIKey() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.key
}

// GetMaskedKey returns a masked version: начало ключа + точки (e.g. sk-proj-abc...••••••••).
func (r *RuntimeStore) GetMaskedKey() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	k := r.key
	if len(k) < 8 {
		return "••••••••"
	}
	n := 12
	if len(k) < n {
		n = len(k)
	}
	prefix := k[:n]
	return prefix + "••••••••"
}

// IsConfigured returns true if a key is set.
func (r *RuntimeStore) IsConfigured() bool {
	return r.GetOpenAIKey() != ""
}

// KeyStore returns a storage.KeyStore that uses this runtime key.
func (r *RuntimeStore) KeyStore() storage.KeyStore {
	return &runtimeKeyStore{r}
}

type runtimeKeyStore struct {
	r *RuntimeStore
}

func (s *runtimeKeyStore) GetByApertureKey(ctx context.Context, apertureKey string) (*storage.Key, error) {
	key := s.r.GetOpenAIKey()
	if key == "" {
		return nil, storage.ErrKeyNotFound
	}
	return &storage.Key{
		ID:           "runtime",
		ApertureKey:  apertureKey,
		OpenAIAPIKey: key,
		Name:         "default",
	}, nil
}

func (s *runtimeKeyStore) Create(ctx context.Context, apertureKey, openaiAPIKey, name string) (*storage.Key, error) {
	return nil, storage.ErrNotSupported
}

func (s *runtimeKeyStore) List(ctx context.Context) ([]storage.Key, error) {
	return nil, nil
}

func (s *runtimeKeyStore) Delete(ctx context.Context, id string) error {
	return storage.ErrNotSupported
}
