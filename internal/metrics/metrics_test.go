package metrics

import (
	"strconv"
	"strings"
	"testing"
)

func render(r *Registry) string {
	var b strings.Builder
	r.Render(&b)
	return b.String()
}

func TestCountersRender(t *testing.T) {
	r := New()
	r.ObserveHTTP("/v1/chat/completions", 200, 0.12)
	r.ObserveHTTP("/v1/chat/completions", 200, 0.30)
	r.ObserveHTTP("/v1/chat/completions", 403, 0.001)
	r.ObserveLLM("openai", "gpt-4o-mini", 200, 100, 40, 0.0006)
	r.ObserveDLP("aws-access-key", "blocked")
	r.ObserveLimitDenied("budget")

	out := render(r)
	for _, want := range []string{
		`aperture_http_requests_total{path="/v1/chat/completions",status="200"} 2`,
		`aperture_http_requests_total{path="/v1/chat/completions",status="403"} 1`,
		`aperture_llm_requests_total{provider="openai",model="gpt-4o-mini",status="200"} 1`,
		`aperture_tokens_total{direction="prompt"} 100`,
		`aperture_tokens_total{direction="completion"} 40`,
		`aperture_dlp_events_total{rule="aws-access-key",action="blocked"} 1`,
		`aperture_limit_denied_total{reason="budget"} 1`,
		"aperture_http_request_duration_seconds_count 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing series %q in:\n%s", want, out)
		}
	}
}

// Histogram buckets are cumulative: every observation counts in each bucket
// at or above its value.
func TestHistogramBucketsAreCumulative(t *testing.T) {
	r := New()
	r.ObserveHTTP("/x", 200, 0.001) // <= every bucket
	r.ObserveHTTP("/x", 200, 40)    // only the 60s bucket
	out := render(r)

	if !strings.Contains(out, `aperture_http_request_duration_seconds_bucket{le="0.005"} 1`) {
		t.Errorf("small bucket wrong:\n%s", out)
	}
	if !strings.Contains(out, `aperture_http_request_duration_seconds_bucket{le="60"} 2`) {
		t.Errorf("large bucket wrong:\n%s", out)
	}
	if !strings.Contains(out, `aperture_http_request_duration_seconds_bucket{le="+Inf"} 2`) {
		t.Errorf("+Inf bucket wrong:\n%s", out)
	}
}

// Model names come from callers, so the series count must stay bounded.
func TestSeriesCountIsBounded(t *testing.T) {
	r := New()
	for i := 0; i < maxSeries*3; i++ {
		r.ObserveLLM("openai", "model-"+strconv.Itoa(i), 200, 1, 1, 0)
	}
	out := render(r)
	got := strings.Count(out, "aperture_llm_requests_total{")
	if got > maxSeries+1 { // +1 for the "other" overflow series
		t.Errorf("series count unbounded: %d", got)
	}
	if !strings.Contains(out, `model="other"`) && !strings.Contains(out, `provider="other"`) {
		t.Errorf("overflow not folded into an 'other' series:\n%s", out[:400])
	}
}

// Label values are caller-controlled, so quotes must not break the format.
func TestLabelValuesAreEscaped(t *testing.T) {
	r := New()
	r.ObserveLLM("openai", `weird"model`, 200, 1, 1, 0)
	out := render(r)
	if !strings.Contains(out, `model="weird\"model"`) {
		t.Errorf("quote not escaped:\n%s", out)
	}
}

// A nil registry is a no-op, so metrics can be switched off without nil checks
// at every call site.
func TestNilRegistryIsSafe(t *testing.T) {
	var r *Registry
	r.ObserveHTTP("/x", 200, 1)
	r.ObserveLLM("openai", "m", 200, 1, 1, 1)
	r.ObserveDLP("rule", "blocked")
	r.ObserveLimitDenied("rate")
}
