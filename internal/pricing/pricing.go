package pricing

// ModelPrice holds per-million-token prices for a model.
type ModelPrice struct {
	InputPerM  float64 // USD per 1M input tokens
	OutputPerM float64 // USD per 1M output tokens
}

// table maps model name (or prefix) to pricing.
// Prices are in USD per 1M tokens as of 2025.
var table = map[string]ModelPrice{
	// OpenAI
	"gpt-4o":                  {InputPerM: 2.50, OutputPerM: 10.00},
	"gpt-4o-mini":             {InputPerM: 0.15, OutputPerM: 0.60},
	"gpt-4-turbo":             {InputPerM: 10.00, OutputPerM: 30.00},
	"gpt-4":                   {InputPerM: 30.00, OutputPerM: 60.00},
	"gpt-3.5-turbo":           {InputPerM: 0.50, OutputPerM: 1.50},
	"o1":                      {InputPerM: 15.00, OutputPerM: 60.00},
	"o1-mini":                 {InputPerM: 3.00, OutputPerM: 12.00},
	// Anthropic
	"claude-sonnet-4":         {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-sonnet":       {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-5-haiku":        {InputPerM: 0.80, OutputPerM: 4.00},
	"claude-3-opus":           {InputPerM: 15.00, OutputPerM: 75.00},
	"claude-3-sonnet":         {InputPerM: 3.00, OutputPerM: 15.00},
	"claude-3-haiku":          {InputPerM: 0.25, OutputPerM: 1.25},
	// Groq (Llama / Mixtral)
	"llama-3.3-70b":           {InputPerM: 0.59, OutputPerM: 0.79},
	"llama-3.1-70b":           {InputPerM: 0.59, OutputPerM: 0.79},
	"llama-3.1-8b":            {InputPerM: 0.05, OutputPerM: 0.08},
	"mixtral-8x7b":            {InputPerM: 0.24, OutputPerM: 0.24},
}

// Calculate returns the cost in USD for the given token counts.
// Model name is matched by prefix so "gpt-4o-mini-2024-07-18" → "gpt-4o-mini".
func Calculate(model string, promptTokens, completionTokens int) float64 {
	p := lookup(model)
	if p == nil {
		return 0
	}
	return float64(promptTokens)/1_000_000*p.InputPerM +
		float64(completionTokens)/1_000_000*p.OutputPerM
}

// lookup finds the best matching price by trying progressively shorter prefixes.
func lookup(model string) *ModelPrice {
	// Exact match first.
	if p, ok := table[model]; ok {
		return &p
	}
	// Prefix match: strip trailing version suffixes like "-20241022", "-2024-07-18".
	candidate := model
	for {
		idx := lastDash(candidate)
		if idx < 0 {
			break
		}
		candidate = candidate[:idx]
		if p, ok := table[candidate]; ok {
			return &p
		}
	}
	return nil
}

func lastDash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '-' {
			return i
		}
	}
	return -1
}