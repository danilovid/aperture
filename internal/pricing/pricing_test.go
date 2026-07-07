package pricing

import (
	"math"
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
		{"claude-3-5-sonnet-20241022", 0, 1_000_000, 15.00},
		{"llama-3.3-70b-versatile", 1_000_000, 0, 0.59},
		{"unknown-model", 1_000_000, 1_000_000, 0}, // unpriced → 0
	}
	for _, c := range cases {
		got := Calculate(c.model, c.prompt, c.complete)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Calculate(%q, %d, %d) = %v, want %v", c.model, c.prompt, c.complete, got, c.want)
		}
	}
}
