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
	Mode           string  `json:"mode,omitempty"`
	InputPerM      float64 `json:"input_per_m,omitempty"`
	OutputPerM     float64 `json:"output_per_m,omitempty"`
	CacheReadPerM  float64 `json:"cache_read_per_m,omitempty"`
	CacheWritePerM float64 `json:"cache_write_per_m,omitempty"`
	// ContextTokens is the input window, MaxOutputTokens the longest answer.
	ContextTokens   int `json:"context_tokens,omitempty"`
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
}

// retired are models the catalog has dropped. Traffic to them still happens,
// and still costs what it cost.
var retired = map[string]Model{
	"claude-opus-4":     {Provider: "anthropic", Mode: "chat", InputPerM: 15.00, OutputPerM: 75.00},
	"claude-sonnet-4":   {Provider: "anthropic", Mode: "chat", InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-7-sonnet": {Provider: "anthropic", Mode: "chat", InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-sonnet": {Provider: "anthropic", Mode: "chat", InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-haiku":  {Provider: "anthropic", Mode: "chat", InputPerM: 0.80, OutputPerM: 4.00},
	"claude-3-opus":     {Provider: "anthropic", Mode: "chat", InputPerM: 15.00, OutputPerM: 75.00},
	"claude-3-sonnet":   {Provider: "anthropic", Mode: "chat", InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-haiku":    {Provider: "anthropic", Mode: "chat", InputPerM: 0.25, OutputPerM: 1.25},
	"llama-3.3-70b":     {Provider: "groq", Mode: "chat", InputPerM: 0.59, OutputPerM: 0.79},
	"llama-3.1-70b":     {Provider: "groq", Mode: "chat", InputPerM: 0.59, OutputPerM: 0.79},
	"llama-3.1-8b":      {Provider: "groq", Mode: "chat", InputPerM: 0.05, OutputPerM: 0.08},
	"mixtral-8x7b":      {Provider: "groq", Mode: "chat", InputPerM: 0.24, OutputPerM: 0.24},
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

// Calculate returns the cost in USD for the given token counts, or 0 for a
// model the catalog does not price.
func Calculate(model string, promptTokens, completionTokens int) float64 {
	m, ok := Lookup(model)
	if !ok {
		return 0
	}
	return float64(promptTokens)/1_000_000*m.InputPerM +
		float64(completionTokens)/1_000_000*m.OutputPerM
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
