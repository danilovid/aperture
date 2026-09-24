package server

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/danilovid/mutegate/internal/storage"
)

// providerPreset is one entry of the console's preset list.
type providerPreset struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	BaseURL  string   `json:"base_url"`
	Prefixes []string `json:"prefixes"`
	Docs     string   `json:"docs"`
	KeyHint  string   `json:"key_hint"`
	APIKey   string   `json:"api_key"`
	Note     string   `json:"note"`
}

// The console's provider presets are data the server never sees until
// somebody saves one. This holds them to the rules a save is held to, so a
// preset never offers what the gateway would refuse — or take traffic that
// already has a home.
func TestProviderPresets(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/console/providerPresets.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var presets []providerPreset
	if err := dec.Decode(&presets); err != nil {
		t.Fatalf("providerPresets.json: %v", err)
	}
	if len(presets) == 0 {
		t.Fatal("no presets")
	}

	builtinTraffic := builtinRoutedModels(t)
	names := map[string]bool{}
	// Two providers cannot share a prefix, so no two presets do: any of them
	// can be added next to any other.
	owner := map[string]string{}
	for _, p := range presets {
		if names[p.Name] {
			t.Errorf("%s: two presets with one name", p.Name)
		}
		names[p.Name] = true
		if p.Label == "" || p.KeyHint == "" {
			t.Errorf("%s: a preset needs a label and a key hint", p.Name)
		}
		if u, err := url.Parse(p.Docs); err != nil || u.Scheme != "https" {
			t.Errorf("%s: docs %q is not an https link", p.Name, p.Docs)
		}

		// It saves: the same checks PUT /admin/providers/{name} runs.
		kind := string(storage.KindCompatible)
		prefixes := p.Prefixes
		if _, err := buildProvider(p.Name, providerRequest{Kind: &kind, BaseURL: &p.BaseURL, Prefixes: &prefixes}, nil); err != nil {
			t.Errorf("%s: the server would refuse it: %v", p.Name, err)
		}

		// It takes no model the built-in routes already send to their own
		// provider: custom prefixes are matched first, so one that did would
		// quietly move that traffic.
		seen := map[string]bool{}
		for _, pfx := range p.Prefixes {
			if pfx != strings.ToLower(strings.TrimSpace(pfx)) || pfx == "" {
				t.Errorf("%s: prefix %q is not lower-case and trimmed", p.Name, pfx)
			}
			if seen[pfx] {
				t.Errorf("%s: prefix %q twice", p.Name, pfx)
			}
			seen[pfx] = true
			if other, ok := owner[pfx]; ok && other != p.Name {
				t.Errorf("%s: prefix %q is %s's too", p.Name, pfx, other)
			}
			owner[pfx] = p.Name
			if llm := modelToLLM(pfx); llm != "openai" {
				t.Errorf("%s: prefix %q is the %s route's own", p.Name, pfx, llm)
			}
			for _, model := range builtinTraffic {
				if strings.HasPrefix(model, pfx) {
					t.Errorf("%s: prefix %q would take %s from the built-in route", p.Name, pfx, model)
					break
				}
			}
		}
	}
}

// builtinRoutedModels are the catalog's models that the built-in routes send
// to the provider that makes them — the traffic a preset must leave alone.
func builtinRoutedModels(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../pricing/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Models map[string]struct {
			Provider string `json:"provider"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	var out []string
	for name, m := range c.Models {
		if modelToLLM(name) == m.Provider {
			out = append(out, name)
		}
	}
	// And the families the routes are written for, whatever the catalog holds.
	return append(out, "claude-", "llama-", "mixtral-", "gpt-", "o1", "o3", "o4-mini")
}
