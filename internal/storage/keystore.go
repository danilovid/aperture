package storage

import (
	"context"
	"errors"
)

var (
	ErrKeyNotFound  = errors.New("key not found")
	ErrNotSupported = errors.New("operation not supported")
)

// Key represents a stored API key mapping.
type Key struct {
	ID           string `json:"id"`
	ApertureKey  string `json:"aperture_key"`  // key clients use (Bearer token)
	OpenAIAPIKey string `json:"openai_api_key"` // upstream OpenAI key
	Name         string `json:"name"`
	CreatedAt    string `json:"created_at"`
}

// KeyStore provides persistence for API keys.
type KeyStore interface {
	// GetByApertureKey returns the OpenAI key for the given aperture key.
	GetByApertureKey(ctx context.Context, apertureKey string) (*Key, error)
	// Create adds a new key mapping.
	Create(ctx context.Context, apertureKey, openaiAPIKey, name string) (*Key, error)
	// List returns all keys (without openai_api_key for security).
	List(ctx context.Context) ([]Key, error)
	// Delete removes a key by ID.
	Delete(ctx context.Context, id string) error
}
