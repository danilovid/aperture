// Package metrics collects gateway counters and renders them in the
// Prometheus text exposition format.
//
// The exposition is hand-written rather than pulled from a client library:
// this project keeps its dependency list to pgx and uuid, and the handful of
// series below do not justify a new one.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// maxSeries bounds the label sets tracked per metric. Model names come from
// callers, so an unbounded map would let one client blow up the series count;
// everything past the cap is folded into label value "other".
const maxSeries = 500

// Buckets for request duration, in seconds. Covers a fast local block through
// a slow reasoning model.
var durationBuckets = []float64{0.005, 0.025, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60}

// The NER stage is supposed to stay in the tens of milliseconds, so its
// buckets are much finer than the request ones.
var nerBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

type histogram struct {
	buckets []float64
	counts  []uint64
	sum     float64
	total   uint64
}

func (h *histogram) observe(v float64) {
	if h.counts == nil {
		h.counts = make([]uint64, len(h.buckets))
	}
	for i, b := range h.buckets {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.total++
}

// Registry holds every counter the gateway exposes. Safe for concurrent use.
type Registry struct {
	mu sync.Mutex

	httpRequests map[string]uint64 // path|status
	httpDuration histogram

	llmRequests map[string]uint64  // provider|model|status
	tokens      map[string]uint64  // direction
	cost        map[string]float64 // provider

	dlpEvents   map[string]uint64 // rule|action
	limitDenied map[string]uint64 // reason

	nerRequests map[string]uint64 // status
	nerLatency  histogram
}

// New creates an empty registry.
func New() *Registry {
	return &Registry{
		httpRequests: map[string]uint64{},
		llmRequests:  map[string]uint64{},
		tokens:       map[string]uint64{},
		cost:         map[string]float64{},
		dlpEvents:    map[string]uint64{},
		limitDenied:  map[string]uint64{},
		nerRequests:  map[string]uint64{},
		httpDuration: histogram{buckets: durationBuckets},
		nerLatency:   histogram{buckets: nerBuckets},
	}
}

// bump increments a series, folding overflow into "other" so caller-supplied
// labels cannot grow the map without bound.
func bump[T uint64 | float64](m map[string]T, key string, by T) {
	if _, ok := m[key]; !ok && len(m) >= maxSeries {
		key = "other"
	}
	m[key] += by
}

// ObserveHTTP records one served request.
func (r *Registry) ObserveHTTP(path string, status int, seconds float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bump(r.httpRequests, path+"|"+strconv.Itoa(status), 1)
	r.httpDuration.observe(seconds)
}

// ObserveLLM records one completed upstream call.
func (r *Registry) ObserveLLM(provider, model string, status, promptTokens, completionTokens int, costUSD float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bump(r.llmRequests, provider+"|"+model+"|"+strconv.Itoa(status), 1)
	bump(r.tokens, "prompt", uint64(max(promptTokens, 0)))
	bump(r.tokens, "completion", uint64(max(completionTokens, 0)))
	if costUSD > 0 {
		bump(r.cost, provider, costUSD)
	}
}

// ObserveDLP records one DLP event.
// ObserveNER records one call to the model stage: status is "ok" or "error".
func (r *Registry) ObserveNER(status string, seconds float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bump(r.nerRequests, status, 1)
	r.nerLatency.observe(seconds)
}

func (r *Registry) ObserveDLP(rule, action string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bump(r.dlpEvents, rule+"|"+action, 1)
}

// ObserveLimitDenied records a request refused by a budget or rate ceiling.
func (r *Registry) ObserveLimitDenied(reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bump(r.limitDenied, reason, 1)
}

// escape quotes a Prometheus label value.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return strings.ReplaceAll(s, "\n", `\n`)
}

// writeCounter renders one counter family. names maps each key segment to a
// label name; keys are the "a|b|c" composites used above.
func writeCounter[T uint64 | float64](w io.Writer, metric, help string, m map[string]T, names ...string) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", metric, help, metric)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.Split(k, "|")
		labels := make([]string, 0, len(names))
		for i, name := range names {
			v := "other"
			if i < len(parts) {
				v = parts[i]
			}
			labels = append(labels, fmt.Sprintf(`%s="%s"`, name, escape(v)))
		}
		switch v := any(m[k]).(type) {
		case uint64:
			fmt.Fprintf(w, "%s{%s} %d\n", metric, strings.Join(labels, ","), v)
		case float64:
			fmt.Fprintf(w, "%s{%s} %g\n", metric, strings.Join(labels, ","), v)
		}
	}
}

// Render writes the whole registry in the Prometheus text format.
// (Not named WriteTo: that name implies the io.WriterTo signature.)
func (r *Registry) Render(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	writeCounter(w, "aperture_http_requests_total",
		"Requests served by the gateway.", r.httpRequests, "path", "status")

	writeHistogram(w, "aperture_http_request_duration_seconds", "Request latency.", r.httpDuration)

	writeCounter(w, "aperture_llm_requests_total",
		"Upstream provider calls.", r.llmRequests, "provider", "model", "status")
	writeCounter(w, "aperture_tokens_total",
		"Tokens proxied, by direction.", r.tokens, "direction")
	writeCounter(w, "aperture_cost_usd_total",
		"Estimated spend in USD.", r.cost, "provider")
	writeCounter(w, "aperture_dlp_events_total",
		"DLP findings, by rule and action.", r.dlpEvents, "rule", "action")
	writeCounter(w, "aperture_limit_denied_total",
		"Requests refused by a budget or rate limit.", r.limitDenied, "reason")
	writeCounter(w, "aperture_ner_requests_total",
		"Calls to the NER model service.", r.nerRequests, "status")
	writeHistogram(w, "aperture_ner_latency_seconds", "NER model service latency.", r.nerLatency)
}

// writeHistogram renders one histogram with cumulative buckets.
func writeHistogram(w io.Writer, metric, help string, h histogram) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", metric, help, metric)
	var cumulative uint64
	for i, b := range h.buckets {
		if h.counts != nil {
			cumulative = h.counts[i]
		}
		fmt.Fprintf(w, "%s_bucket{le=\"%g\"} %d\n", metric, b, cumulative)
	}
	fmt.Fprintf(w, "%s_bucket{le=\"+Inf\"} %d\n", metric, h.total)
	fmt.Fprintf(w, "%s_sum %g\n", metric, h.sum)
	fmt.Fprintf(w, "%s_count %d\n", metric, h.total)
}
