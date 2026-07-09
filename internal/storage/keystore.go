package storage

import (
	"context"
	"errors"
)

var (
	ErrKeyNotFound  = errors.New("key not found")
	ErrKeyExists    = errors.New("key already exists")
	ErrInvalidInput = errors.New("invalid input")
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

// KeyCreateInput is payload for creating a new aperture key.
type KeyCreateInput struct {
	ApertureKey string
	Name        string
	Providers   map[string]string
}

// KeyStore provides persistence for API keys.
type KeyStore interface {
	// GetByApertureKey returns the key with all provider keys for the given aperture token.
	GetByApertureKey(ctx context.Context, apertureKey string) (*Key, error)
	// Create creates a new aperture key with optional provider keys.
	Create(ctx context.Context, in KeyCreateInput) (*Key, error)
	// List returns all aperture keys (without provider key values).
	List(ctx context.Context) ([]Key, error)
	// Delete removes a key by ID.
	Delete(ctx context.Context, id string) error

	// SetProviderKeys upserts the default aperture key with the given provider keys.
	// Only non-empty values are updated; existing keys for other providers are preserved.
	SetProviderKeys(ctx context.Context, providers map[string]string) error
	// GetProviderKeys returns provider keys for the default aperture key.
	GetProviderKeys(ctx context.Context) (map[string]string, error)
	// ClearProviderKeys removes all provider keys for the default aperture key.
	ClearProviderKeys(ctx context.Context) error
}
