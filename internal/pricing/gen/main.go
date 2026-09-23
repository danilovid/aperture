// Command gen rebuilds internal/pricing/catalog.json from LiteLLM's public
// model catalog:
//
//	go generate ./internal/pricing
//
// It is run by hand, never by the build. The catalog is committed, so the
// gateway prices requests where there is no internet, and a change in prices
// arrives as a diff somebody reads before it ships.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	repo = "BerriAI/litellm"
	file = "model_prices_and_context_window.json"
)

// providers are the catalog's providers worth pricing, in the order that
// settles a clash: when two of them list the same model name, the one that
// makes the model wins over one that resells it. Besides the built-ins, these
// are the upstreams people add as OpenAI-compatible providers.
var providers = []string{
	"openai", "anthropic", "groq",
	"deepseek", "mistral", "xai", "gemini", "zai", "moonshot", "dashscope",
}

// datedSuffix is a snapshot date on a model id: -20250929 or -2025-08-07.
var datedSuffix = regexp.MustCompile(`-(\d{8}|\d{4}-\d{2}-\d{2})$`)

// entry mirrors pricing.Model; TestCatalogLoads keeps the two in step.
type entry struct {
	Provider         string  `json:"provider"`
	Mode             string  `json:"mode,omitempty"`
	InputPerM        float64 `json:"input_per_m,omitempty"`
	OutputPerM       float64 `json:"output_per_m,omitempty"`
	CacheReadPerM    float64 `json:"cache_read_per_m,omitempty"`
	CacheWritePerM   float64 `json:"cache_write_per_m,omitempty"`
	CacheWrite1hPerM float64 `json:"cache_write_1h_per_m,omitempty"`
	ContextTokens    int     `json:"context_tokens,omitempty"`
	MaxOutputTokens  int     `json:"max_output_tokens,omitempty"`
}

// source is one model as LiteLLM lists it. Prices are USD per token.
type source struct {
	Provider        string  `json:"litellm_provider"`
	Mode            string  `json:"mode"`
	Input           float64 `json:"input_cost_per_token"`
	Output          float64 `json:"output_cost_per_token"`
	CacheRead       float64 `json:"cache_read_input_token_cost"`
	CacheWrite      float64 `json:"cache_creation_input_token_cost"`
	CacheWrite1h    float64 `json:"cache_creation_input_token_cost_above_1hr"`
	MaxInputTokens  int     `json:"max_input_tokens"`
	MaxTokens       int     `json:"max_tokens"`
	MaxOutputTokens int     `json:"max_output_tokens"`
}

func main() {
	ref := flag.String("ref", "main", "branch, tag or commit of "+repo+" to read")
	out := flag.String("out", "catalog.json", "where to write the catalog")
	flag.Parse()

	sha, err := resolve(*ref)
	if err != nil {
		log.Fatalf("resolve %s: %v", *ref, err)
	}
	raw, err := get("https://raw.githubusercontent.com/" + repo + "/" + sha + "/" + file)
	if err != nil {
		log.Fatalf("download: %v", err)
	}
	models, err := build(raw)
	if err != nil {
		log.Fatal(err)
	}
	body := render(models, sha)
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("%d models from %s@%s → %s", len(models), repo, sha[:7], *out)
}

// resolve pins ref to a commit, so the catalog says exactly what it was
// built from and a rebuild from the same commit gives the same file.
func resolve(ref string) (string, error) {
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+repo+"/commits/"+ref, nil)
	req.Header.Set("Accept", "application/vnd.github.sha")
	b, err := fetch(req)
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(string(b))
	if len(sha) != 40 {
		return "", fmt.Errorf("unexpected answer %q", sha)
	}
	return sha, nil
}

func get(url string) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	return fetch(req)
}

func fetch(req *http.Request) ([]byte, error) {
	c := &http.Client{Timeout: time.Minute}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", req.URL, resp.Status)
	}
	return b, nil
}

// build picks the providers' models out of LiteLLM's catalog, names them the
// way a client asks for them, and converts prices to USD per million tokens.
func build(raw []byte) (map[string]entry, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	rank := map[string]int{}
	for i, p := range providers {
		rank[p] = i
	}

	type pick struct {
		entry
		key string
	}
	// better settles two listings of one name: the maker over a reseller;
	// within one provider, the prefixed key, which is LiteLLM's current
	// spelling; then the key itself, so every rebuild picks the same one.
	better := func(a, b pick) bool {
		if ra, rb := rank[a.Provider], rank[b.Provider]; ra != rb {
			return ra < rb
		}
		if pa, pb := strings.Contains(a.key, "/"), strings.Contains(b.key, "/"); pa != pb {
			return pa
		}
		return a.key < b.key
	}
	picked := map[string]pick{}
	for key, msg := range all {
		var s source
		if json.Unmarshal(msg, &s) != nil {
			continue // sample_spec and anything else that is not a model
		}
		if _, ok := rank[s.Provider]; !ok {
			continue
		}
		// A client asks Groq for "openai/gpt-oss-120b", not
		// "groq/openai/gpt-oss-120b": the provider prefix is LiteLLM's own.
		name := strings.ToLower(strings.TrimPrefix(key, s.Provider+"/"))
		p := pick{key: key, entry: entry{
			Provider:         s.Provider,
			Mode:             s.Mode,
			InputPerM:        perMillion(s.Input),
			OutputPerM:       perMillion(s.Output),
			CacheReadPerM:    perMillion(s.CacheRead),
			CacheWritePerM:   perMillion(s.CacheWrite),
			CacheWrite1hPerM: perMillion(s.CacheWrite1h),
			ContextTokens:    firstNonZero(s.MaxInputTokens, s.MaxTokens),
			MaxOutputTokens:  s.MaxOutputTokens,
		}}
		if prev, ok := picked[name]; ok && better(prev, p) {
			continue
		}
		picked[name] = p
	}

	models := make(map[string]entry, len(picked))
	names := make([]string, 0, len(picked))
	for name, p := range picked {
		models[name] = p.entry
		names = append(names, name)
	}
	// Clients also ask for a dated snapshot by its undated name. Where the
	// catalog has no such name, it goes to the latest snapshot — descending
	// order meets that one first.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		if alias := datedSuffix.ReplaceAllString(name, ""); alias != name {
			if _, ok := models[alias]; !ok {
				models[alias] = models[name]
			}
		}
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("no models found — has the catalog's format changed?")
	}
	return models, nil
}

// perMillion turns a per-token price into a per-million one, rounded to
// hide float noise (0.15000000000000002).
func perMillion(perToken float64) float64 {
	return math.Round(perToken*1e6*1e6) / 1e6
}

func firstNonZero(vs ...int) int {
	for _, v := range vs {
		if v != 0 {
			return v
		}
	}
	return 0
}

// render writes one model per line, sorted, so a rebuild reads as a diff of
// the prices that changed.
func render(models map[string]entry, sha string) []byte {
	names := make([]string, 0, len(models))
	for n := range models {
		names = append(names, n)
	}
	sort.Strings(names)

	var b bytes.Buffer
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"source\": %q,\n", "https://github.com/"+repo+"/blob/"+sha+"/"+file)
	b.WriteString("  \"license\": \"MIT, Copyright (c) 2023 Berri AI\",\n")
	b.WriteString("  \"models\": {\n")
	for i, n := range names {
		k, _ := json.Marshal(n)
		v, _ := json.Marshal(models[n])
		b.WriteString("    ")
		b.Write(k)
		b.WriteString(": ")
		b.Write(v)
		if i < len(names)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  }\n}\n")
	return b.Bytes()
}
