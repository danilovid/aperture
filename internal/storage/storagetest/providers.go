package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/mutegate/mutegate/internal/storage"
)

// RunProviderStore holds every ProviderStore to the same contract. The
// organizations it uses must exist where that matters (see OrgA and OrgB).
func RunProviderStore(t *testing.T, newStore func(t *testing.T) storage.ProviderStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("a provider round-trips, secrets included", func(t *testing.T) {
		s := newStore(t)
		in := storage.ProviderConfig{
			Name: "deepseek", Kind: storage.KindCompatible, BaseURL: "https://api.deepseek.com/v1",
			APIKey: "sk-deep", ProxyURL: "http://user:pass@proxy.corp:3128",
			Prefixes: []string{"deepseek-"}, TimeoutMS: 90000, Enabled: true,
		}
		if err := s.PutProvider(ctx, OrgA, in); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetProvider(ctx, OrgA, "deepseek")
		if err != nil {
			t.Fatal(err)
		}
		if got.APIKey != "sk-deep" || got.ProxyURL != in.ProxyURL || got.BaseURL != in.BaseURL ||
			got.Kind != storage.KindCompatible || len(got.Prefixes) != 1 || got.TimeoutMS != 90000 || !got.Enabled {
			t.Errorf("provider came back different: %+v", got)
		}
		if got.UpdatedAt.IsZero() {
			t.Error("no updated time")
		}
	})

	t.Run("putting again replaces", func(t *testing.T) {
		s := newStore(t)
		s.PutProvider(ctx, OrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, APIKey: "old", Enabled: true})
		s.PutProvider(ctx, OrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, APIKey: "new", Enabled: false})
		got, _ := s.GetProvider(ctx, OrgA, "openai")
		if got.APIKey != "new" || got.Enabled {
			t.Errorf("replace kept the old values: %+v", got)
		}
		list, _ := s.ListProviders(ctx, OrgA)
		if len(list) != 1 {
			t.Errorf("replace added a second row: %d", len(list))
		}
	})

	t.Run("one organization's providers are not another's", func(t *testing.T) {
		s := newStore(t)
		s.PutProvider(ctx, OrgA, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, APIKey: "sk-a", Enabled: true})
		s.PutProvider(ctx, OrgB, storage.ProviderConfig{Name: "openai", Kind: storage.KindOpenAI, APIKey: "sk-b", Enabled: true})

		a, _ := s.GetProvider(ctx, OrgA, "openai")
		b, _ := s.GetProvider(ctx, OrgB, "openai")
		if a.APIKey != "sk-a" || b.APIKey != "sk-b" {
			t.Fatalf("keys crossed organizations: A has %q, B has %q", a.APIKey, b.APIKey)
		}
		if err := s.DeleteProvider(ctx, OrgA, "openai"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetProvider(ctx, OrgB, "openai"); err != nil {
			t.Error("deleting A's provider removed B's")
		}
		if list, _ := s.ListProviders(ctx, OrgA); len(list) != 0 {
			t.Errorf("A still lists %d providers", len(list))
		}
	})

	t.Run("missing is ErrProviderNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.GetProvider(ctx, OrgA, "nothing"); !errors.Is(err, storage.ErrProviderNotFound) {
			t.Errorf("get = %v", err)
		}
		if err := s.DeleteProvider(ctx, OrgA, "nothing"); !errors.Is(err, storage.ErrProviderNotFound) {
			t.Errorf("delete = %v", err)
		}
	})
}
