package pricing

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCalculate(t *testing.T) {
	cases := []struct {
		model            string
		prompt, complete int
		want             float64
	}{
		{"gpt-4o-mini", 1_000_000, 1_000_000, 0.15 + 0.60},
		{"gpt-4o-mini-2024-07-18", 1_000_000, 0, 0.15}, // version suffix stripped
		{"GPT-4o-mini", 1_000_000, 0, 0.15},            // names are case-insensitive
		{"claude-3-5-sonnet-20241022", 0, 1_000_000, 15.00},
		{"llama-3.3-70b-versatile", 1_000_000, 0, 0.59},
		{"unknown-model", 1_000_000, 1_000_000, 0}, // unpriced → 0
	}
	for _, c := range cases {
		got := Cost(c.model, Usage{PromptTokens: c.prompt, CompletionTokens: c.complete})
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Cost(%q, %d in, %d out) = %v, want %v", c.model, c.prompt, c.complete, got, c.want)
		}
	}
}

// Cached input is input, priced at the cache's rates: a read at a tenth of a
// plain token, a write a quarter more, a one-hour write double. The retired
// Claude entries follow Anthropic's rules exactly, so the sums are fixed.
func TestCostWithCache(t *testing.T) {
	cases := []struct {
		name  string
		model string
		u     Usage
		want  float64
	}{
		{"reads", "claude-3-5-sonnet", Usage{PromptTokens: 1_001_000, CacheReadTokens: 1_000_000, CompletionTokens: 1_000},
			(1_000*3.00 + 1_000_000*0.30 + 1_000*15.00) / 1e6},
		{"writes, some for an hour", "claude-3-5-sonnet", Usage{PromptTokens: 100_000, CacheWriteTokens: 100_000, CacheWrite1hTokens: 40_000},
			(60_000*3.75 + 40_000*6.00) / 1e6},
		{"a catalog model", "gpt-4o-mini", Usage{PromptTokens: 1_000_000, CacheReadTokens: 1_000_000},
			0.075},
		// Without a cache price, cached tokens cost what plain input does.
		{"no cache price", "llama-3.3-70b", Usage{PromptTokens: 1_000_000, CacheReadTokens: 500_000},
			0.59},
		// Counts that do not add up are not charged below zero or twice.
		{"inconsistent counts", "claude-3-5-sonnet", Usage{PromptTokens: 10, CacheReadTokens: 1_000_000, CacheWrite1hTokens: 5},
			1_000_000 * 0.30 / 1e6},
	}
	for _, c := range cases {
		if got := Cost(c.model, c.u); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: Cost(%q, %+v) = %v, want %v", c.name, c.model, c.u, got, c.want)
		}
	}
}

// The catalog decodes into Model with nothing left over, so the generator and
// this package agree on every field, and it looks like what gen writes.
func TestCatalogLoads(t *testing.T) {
	dec := json.NewDecoder(bytes.NewReader(catalogJSON))
	dec.DisallowUnknownFields()
	var c struct {
		Source  string           `json:"source"`
		License string           `json:"license"`
		Models  map[string]Model `json:"models"`
	}
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("catalog.json: %v", err)
	}
	if !strings.Contains(c.Source, "github.com/BerriAI/litellm/blob/") || c.License == "" {
		t.Errorf("catalog does not say where it came from: source %q, license %q", c.Source, c.License)
	}
	seen := map[string]int{}
	for name, m := range c.Models {
		seen[m.Provider]++
		if name != strings.ToLower(name) || strings.HasPrefix(name, m.Provider+"/") {
			t.Errorf("%q: names are lower-case, without LiteLLM's provider prefix", name)
		}
		if m.InputPerM < 0 || m.OutputPerM < 0 || m.CacheReadPerM < 0 || m.CacheWritePerM < 0 {
			t.Errorf("%q: negative price %+v", name, m)
		}
	}
	for _, p := range []string{"openai", "anthropic", "groq"} {
		if seen[p] == 0 {
			t.Errorf("no %s models in the catalog", p)
		}
	}
}

// Current models cost money. Before the catalog they were missing from a
// hand-written table and recorded as free.
func TestCurrentModelsArePriced(t *testing.T) {
	for _, model := range []string{
		"gpt-5", "gpt-5-mini", "gpt-4.1", "o3",
		"claude-opus-4-5", "claude-sonnet-4-5-20250929", "claude-haiku-4-5",
		"deepseek-chat", // a custom OpenAI-compatible provider
	} {
		if m, ok := Lookup(model); !ok || m.InputPerM <= 0 || m.OutputPerM <= 0 {
			t.Errorf("%s: %+v, %v — want a price", model, m, ok)
		}
	}
}

func TestLookupPrefersTheExactModel(t *testing.T) {
	// A dated snapshot costs what its undated name does.
	dated, _ := Lookup("claude-sonnet-4-5-20250929")
	undated, _ := Lookup("claude-sonnet-4-5")
	if dated != undated {
		t.Errorf("snapshot %+v differs from its name %+v", dated, undated)
	}
	// A newer version is its own model, not its retired family: Opus 4.5 is
	// a third of Opus 4's price.
	if m, _ := Lookup("claude-opus-4-5"); m.InputPerM == retired["claude-opus-4"].InputPerM {
		t.Errorf("claude-opus-4-5 priced as claude-opus-4: %+v", m)
	}
	// And a smaller sibling is not priced as the big one.
	mini, _ := Lookup("gpt-5-mini")
	full, _ := Lookup("gpt-5")
	if mini.InputPerM >= full.InputPerM {
		t.Errorf("gpt-5-mini (%v) priced like gpt-5 (%v)", mini.InputPerM, full.InputPerM)
	}
}

// The catalog carries more than the two prices Calculate uses today.
func TestCatalogCarriesCacheAndContext(t *testing.T) {
	m, ok := Lookup("claude-haiku-4-5")
	if !ok || m.CacheReadPerM <= 0 || m.CacheWritePerM <= 0 || m.ContextTokens <= 0 || m.Mode != "chat" {
		t.Errorf("claude-haiku-4-5 = %+v, want cache prices, a context window and mode chat", m)
	}
}
