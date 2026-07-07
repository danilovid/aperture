package config

import (
	"context"
	"errors"
	"testing"

	"github.com/danilovid/aperture/internal/storage"
)

func TestRuntimeKeyStoreRejectsWrongToken(t *testing.T) {
	ks := NewRuntimeStore("ap-secret").KeyStore()
	if err := ks.SetProviderKeys(context.Background(), map[string]string{"openai": "sk-x"}); err != nil {
		t.Fatalf("SetProviderKeys: %v", err)
	}

	for _, token := range []string{"", "dev", "ap-wrong", "AP-SECRET"} {
		if _, err := ks.GetByApertureKey(context.Background(), token); !errors.Is(err, storage.ErrKeyNotFound) {
			t.Errorf("token %q: want ErrKeyNotFound, got %v", token, err)
		}
	}

	key, err := ks.GetByApertureKey(context.Background(), "ap-secret")
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if key.Providers["openai"] != "sk-x" {
		t.Errorf("provider key not returned: %v", key.Providers)
	}
}

func TestRuntimeKeyStoreRejectsValidTokenWithoutProviders(t *testing.T) {
	ks := NewRuntimeStore("ap-secret").KeyStore()
	if _, err := ks.GetByApertureKey(context.Background(), "ap-secret"); !errors.Is(err, storage.ErrKeyNotFound) {
		t.Errorf("want ErrKeyNotFound when no providers configured, got %v", err)
	}
}

func TestGenerateKey(t *testing.T) {
	a, b := GenerateKey("ap"), GenerateKey("ap")
	if a == b {
		t.Error("two generated keys are identical")
	}
	if len(a) != len("ap-")+32 {
		t.Errorf("unexpected key length: %q", a)
	}
}
