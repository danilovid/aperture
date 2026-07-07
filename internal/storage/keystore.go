package storage

import (
	"context"
	"errors"
)

var (
	ErrKeyNotFound  = errors.New("key not found")
	ErrNotSupported = errors.New("operation not supported")
)

// Key represents an aperture API key and its associated provider keys.
type Key struct {
	ID          string            `json:"id"`
	ApertureKey string            `json:"aperture_key"`
	Name        string            `json:"name"`
	CreatedAt   string            `json:"created_at"`
	Providers   map[string]string `json:"providers,omitempty"` // "openai" -> "sk-...", "anthropic" -> "sk-ant-..."
}

// KeyStore provides persistence for API keys.
type KeyStore interface {
	// GetByApertureKey returns the key with all provider keys for the given aperture token.
	GetByApertureKey(ctx context.Context, apertureKey string) (*Key, error)
	// Create adds a new aperture key with the given provider keys.
	Create(ctx context.Context, apertureKey, name string, providers map[string]string) (*Key, error)
	// List returns all aperture keys (without provider key values).
	List(ctx context.Context) ([]Key, error)
	// Delete removes a key by ID.
	Delete(ctx context.Context, id string) error

	// SetProviderKeys upserts the default "dev" aperture key with the given provider keys.
	// Only non-empty values are updated; existing keys for other providers are preserved.
	SetProviderKeys(ctx context.Context, providers map[string]string) error
	// GetProviderKeys returns provider keys for the default "dev" aperture key.
	GetProviderKeys(ctx context.Context) (map[string]string, error)
	// ClearProviderKeys removes all provider keys for the default "dev" aperture key.
	ClearProviderKeys(ctx context.Context) error
}
