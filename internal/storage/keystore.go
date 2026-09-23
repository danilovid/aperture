package storage

import (
	"context"
	"errors"
)

var (
	ErrKeyNotFound  = errors.New("key not found")
	ErrNotSupported = errors.New("operation not supported")
)

// Key represents a Mutegate API key and its associated provider keys.
type Key struct {
	ID string `json:"id"`
	// OrgID is the organization this key belongs to. Everything an agent
	// does with it — logs, incidents, spend — lands in that organization.
	OrgID       string            `json:"org_id"`
	MutegateKey string            `json:"mutegate_key"`
	Name        string            `json:"name"`
	CreatedAt   string            `json:"created_at"`
	Providers   map[string]string `json:"providers,omitempty"` // "openai" -> "sk-...", "anthropic" -> "sk-ant-..."
}

// KeyStore provides persistence for API keys.
type KeyStore interface {
	// GetByMutegateKey returns the key with all provider keys for the given mutegate token.
	GetByMutegateKey(ctx context.Context, mutegateKey string) (*Key, error)
	// Create adds a new Mutegate key in an organization.
	Create(ctx context.Context, orgID, mutegateKey, name string, providers map[string]string) (*Key, error)
	// List returns the organization's Mutegate keys (without provider key values).
	List(ctx context.Context, orgID string) ([]Key, error)
	// Delete removes one of the organization's keys.
	Delete(ctx context.Context, orgID, id string) error

	// SetProviderKeys upserts the organization's own provider keys — the ones
	// Settings edits, held on a row no bearer token can authenticate as.
	// Only non-empty values are updated; keys for other providers are kept.
	SetProviderKeys(ctx context.Context, orgID string, providers map[string]string) error
	// GetProviderKeys returns the organization's own provider keys.
	GetProviderKeys(ctx context.Context, orgID string) (map[string]string, error)
	// ClearProviderKeys removes the organization's own provider keys.
	ClearProviderKeys(ctx context.Context, orgID string) error
}
