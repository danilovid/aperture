package server

import "testing"

func TestModelToLLM(t *testing.T) {
	cases := map[string]string{
		"gpt-4o-mini":                "openai",
		"o1":                         "openai",
		"claude-3-5-sonnet-20241022": "anthropic",
		"Claude-Sonnet-4":            "anthropic",
		"llama-3.3-70b-versatile":    "groq",
		"mixtral-8x7b-32768":         "groq",
		"":                           "openai",
	}
	for model, want := range cases {
		if got := modelToLLM(model); got != want {
			t.Errorf("modelToLLM(%q) = %q, want %q", model, got, want)
		}
	}
}
