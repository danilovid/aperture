package anthropic

import "github.com/mutegate/mutegate/internal/pricing"

// Usage is the token block Anthropic reports. InputTokens counts only what
// came after the last cache breakpoint: what was read from the prompt cache,
// or written to it, is reported beside it and is input all the same. Clients
// that cache their prompts — Claude Code does, heavily — send most of their
// input that way.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	// CacheCreation splits the writes by how long the cache keeps them.
	CacheCreation struct {
		Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

// Tokens is u in the gateway's terms, where every input token is counted
// once in PromptTokens, cached or not.
func (u Usage) Tokens() pricing.Usage {
	return pricing.Usage{
		PromptTokens:       u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
		CompletionTokens:   u.OutputTokens,
		CacheReadTokens:    u.CacheReadInputTokens,
		CacheWriteTokens:   u.CacheCreationInputTokens,
		CacheWrite1hTokens: u.CacheCreation.Ephemeral1hInputTokens,
	}
}

// Merge lays a stream's message_delta usage over its message_start usage.
// The delta carries the final output count and, on newer API versions, the
// input and cache counts again; a count it leaves out keeps its first value.
func (u *Usage) Merge(d Usage) {
	u.OutputTokens = d.OutputTokens
	if d.InputTokens > 0 {
		u.InputTokens = d.InputTokens
	}
	if d.CacheCreationInputTokens > 0 {
		u.CacheCreationInputTokens = d.CacheCreationInputTokens
	}
	if d.CacheReadInputTokens > 0 {
		u.CacheReadInputTokens = d.CacheReadInputTokens
	}
	if d.CacheCreation.Ephemeral1hInputTokens > 0 {
		u.CacheCreation.Ephemeral1hInputTokens = d.CacheCreation.Ephemeral1hInputTokens
	}
}

// openAIUsage is u in the shape an OpenAI client — and the interceptor that
// meters spend — reads: cached input inside prompt_tokens, and the part read
// from the cache in prompt_tokens_details. OpenAI has no field for cache
// writes; this path never asks Anthropic to cache, so there are none to lose.
func openAIUsage(u Usage) map[string]any {
	t := u.Tokens()
	out := map[string]any{
		"prompt_tokens":     t.PromptTokens,
		"completion_tokens": t.CompletionTokens,
		"total_tokens":      t.PromptTokens + t.CompletionTokens,
	}
	if t.CacheReadTokens > 0 {
		out["prompt_tokens_details"] = map[string]any{"cached_tokens": t.CacheReadTokens}
	}
	return out
}
