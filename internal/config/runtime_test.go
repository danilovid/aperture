package config

import (
	"context"
	"errors"
	"testing"

	"github.com/danilovid/aperture/internal/storage"
)

func TestRuntimeKeyStoreRequiresMatchingApertureKey(t *testing.T) {
	t.Parallel()

	ks := NewRuntimeStore("runtime-secret").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{
		"openai": "sk-openai-test",
	}); err != nil {
		t.Fatalf("SetProviderKeys() error = %v", err)
	}

	if _, err := ks.GetByApertureKey(context.Background(), "wrong-key"); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Fatalf("GetByApertureKey(wrong) err = %v, want %v", err, storage.ErrKeyNotFound)
	}

	key, err := ks.GetByApertureKey(context.Background(), "runtime-secret")
	if err != nil {
		t.Fatalf("GetByApertureKey(correct) error = %v", err)
	}
	if key.ApertureKey != "runtime-secret" {
		t.Fatalf("ApertureKey = %q, want runtime-secret", key.ApertureKey)
	}
}
