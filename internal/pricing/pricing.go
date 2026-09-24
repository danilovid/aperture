// Package pricing turns token counts into dollars, from a model catalog
// built into the binary.
//
// The catalog is catalog.json, generated from LiteLLM's public model list
// (see gen/) and committed: the gateway prices requests where there is no
// internet, and a price change arrives as a reviewed diff. Rebuild it with
//
//	go generate ./internal/pricing
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:generate go run ./gen

//go:embed catalog.json
var catalogJSON []byte

// Model is what the catalog knows about one model. Prices are USD per
// million tokens; zero means the catalog lists none.
type Model struct {
	Provider string `json:"provider"`
	// Mode is what the model does: chat, responses, embedding,
	// image_generation, audio_speech, audio_transcription, video_generation…
	Mode       string  `json:"mode,omitempty"`
	InputPerM  float64 `json:"input_per_m,omitempty"`
	OutputPerM float64 `json:"output_per_m,omitempty"`
	// A prompt cache is read at CacheReadPerM and written at CacheWritePerM,
	// or at CacheWrite1hPerM for Anthropic's one-hour cache.
	CacheReadPerM    float64 `json:"cache_read_per_m,omitempty"`
	CacheWritePerM   float64 `json:"cache_write_per_m,omitempty"`
	CacheWrite1hPerM float64 `json:"cache_write_1h_per_m,omitempty"`
	// ContextTokens is the input window, MaxOutputTokens the longest answer.
	ContextTokens   int `json:"context_tokens,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

// retired are models the catalog has dropped. Traffic to them still happens,
// and still costs what it cost.
var retired = map[string]Model{
	"claude-opus-4":     claude(15.00, 75.00),
	"claude-sonnet-4":   claude(3.00, 15.00),
	"claude-3-7-sonnet": claude(3.00, 15.00),
	"claude-3-5-sonnet": claude(3.00, 15.00),
	"claude-3-5-haiku":  claude(0.80, 4.00),
	"claude-3-opus":     claude(15.00, 75.00),
	"claude-3-sonnet":   claude(3.00, 15.00),
	"claude-3-haiku":    claude(0.25, 1.25),
	"llama-3.3-70b":     {Provider: "groq", Mode: "chat", InputPerM: 0.59, OutputPerM: 0.79},
	"llama-3.1-70b":     {Provider: "groq", Mode: "chat", InputPerM: 0.59, OutputPerM: 0.79},
	"llama-3.1-8b":      {Provider: "groq", Mode: "chat", InputPerM: 0.05, OutputPerM: 0.08},
	"mixtral-8x7b":      {Provider: "groq", Mode: "chat", InputPerM: 0.24, OutputPerM: 0.24},
}

// claude prices a Claude model the way Anthropic prices all of them: a cache
// read is a tenth of an input token, a write a quarter more, a one-hour write
// double.
func claude(inputPerM, outputPerM float64) Model {
	return Model{
		Provider: "anthropic", Mode: "chat", InputPerM: inputPerM, OutputPerM: outputPerM,
		CacheReadPerM: inputPerM * 0.1, CacheWritePerM: inputPerM * 1.25, CacheWrite1hPerM: inputPerM * 2,
	}
}

var table = load()

func load() map[string]Model {
	var c struct {
		Models map[string]Model `json:"models"`
	}
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		// The catalog is compiled in: a broken one is a broken build, and
		// TestCatalogLoads stops it long before it gets here.
		panic(fmt.Sprintf("pricing: catalog.json: %v", err))
	}
	t := make(map[string]Model, len(c.Models)+len(retired))
	for name, m := range retired {
		t[name] = m
	}
	for name, m := range c.Models {
		t[name] = m
	}
	return t
}

// Usage is what one request consumed. PromptTokens counts every input token,
// cached or not; the cache fields say how many of them the provider read from
// its prompt cache or wrote to it, which cost less and more than the rest.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	CacheReadTokens  int
	// CacheWriteTokens are all tokens written to the cache; CacheWrite1hTokens
	// the part of them written to Anthropic's one-hour cache.
	CacheWriteTokens   int
	CacheWrite1hTokens int
}

// Cost returns what u cost in USD on model, or 0 for a model the catalog does
// not price. A cache price the catalog lacks falls back to the next one that
// applies, and in the end to the plain input price.
func Cost(model string, u Usage) float64 {
	m, ok := Lookup(model)
	if !ok {
		return 0
	}
	read := firstPrice(m.CacheReadPerM, m.InputPerM)
	write := firstPrice(m.CacheWritePerM, m.InputPerM)
	write1h := firstPrice(m.CacheWrite1hPerM, write)

	write1hTokens := min(u.CacheWrite1hTokens, u.CacheWriteTokens)
	plain := max(u.PromptTokens-u.CacheReadTokens-u.CacheWriteTokens, 0)
	return (float64(plain)*m.InputPerM +
		float64(u.CacheReadTokens)*read +
		float64(u.CacheWriteTokens-write1hTokens)*write +
		float64(write1hTokens)*write1h +
		float64(u.CompletionTokens)*m.OutputPerM) / 1_000_000
}

func firstPrice(prices ...float64) float64 {
	for _, p := range prices {
		if p > 0 {
			return p
		}
	}
	return 0
}

// Lookup finds a model by its exact name, or else by the longest known name
// it starts with once trailing parts are dropped: "gpt-4o-mini-2024-07-18"
// finds "gpt-4o-mini". Names are compared case-insensitively.
func Lookup(model string) (Model, bool) {
	candidate := strings.ToLower(model)
	for {
		if m, ok := table[candidate]; ok {
			return m, true
		}
		idx := strings.LastIndexByte(candidate, '-')
		if idx < 0 {
			return Model{}, false
		}
		candidate = candidate[:idx]
	}
}
