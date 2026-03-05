package storage

import (
	"context"
)

// EnvKeyStore returns a fixed OpenAI key for any aperture key lookup.
// Used when DATABASE_URL is not set — backward compatibility with OPENAI_API_KEY.
type EnvKeyStore struct {
	OpenAIAPIKey string
}

// GetByApertureKey implements storage.KeyStore.
func (s *EnvKeyStore) GetByApertureKey(ctx context.Context, apertureKey string) (*Key, error) {
	if s.OpenAIAPIKey == "" {
		return nil, ErrKeyNotFound
	}
	return &Key{
		ID:           "env",
		ApertureKey:  apertureKey,
		OpenAIAPIKey: s.OpenAIAPIKey,
		Name:         "default",
	}, nil
}

// Create is not supported for env store.
func (s *EnvKeyStore) Create(ctx context.Context, apertureKey, openaiAPIKey, name string) (*Key, error) {
	return nil, ErrNotSupported
}

// List returns empty — no keys in env mode.
func (s *EnvKeyStore) List(ctx context.Context) ([]Key, error) {
	return nil, nil
}

// Delete is not supported.
func (s *EnvKeyStore) Delete(ctx context.Context, id string) error {
	return ErrNotSupported
}
