package config

import "testing"

func TestParseCustomProviders(t *testing.T) {
	raw := `[
	  {"name":"DeepSeek","base_url":"https://api.deepseek.com/v1","prefixes":["deepseek"],"api_key":"sk-ds"},
	  {"name":"ollama","base_url":"http://localhost:11434/v1","prefixes":["qwen","llama3.2"],"api_key":"x"}
	]`
	ps, err := parseCustomProviders(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("want 2 providers, got %d", len(ps))
	}
	if ps[0].Name != "deepseek" { // normalized to lower-case
		t.Errorf("name not normalized: %q", ps[0].Name)
	}
	if ps[1].Name != "ollama" || len(ps[1].Prefixes) != 2 {
		t.Errorf("second provider wrong: %+v", ps[1])
	}
}

func TestParseCustomProvidersEmpty(t *testing.T) {
	for _, in := range []string{"", "   "} {
		ps, err := parseCustomProviders(in)
		if err != nil || ps != nil {
			t.Errorf("empty input should yield nil,nil; got %v,%v", ps, err)
		}
	}
}

func TestParseCustomProvidersValidation(t *testing.T) {
	cases := map[string]string{
		"invalid json":      `{not json`,
		"missing name":      `[{"base_url":"https://x","prefixes":["a"]}]`,
		"builtin collision": `[{"name":"openai","base_url":"https://x","prefixes":["a"]}]`,
		"missing base_url":  `[{"name":"x","prefixes":["a"]}]`,
		"missing prefixes":  `[{"name":"x","base_url":"https://x"}]`,
		"duplicate name":    `[{"name":"x","base_url":"https://x","prefixes":["a"]},{"name":"x","base_url":"https://y","prefixes":["b"]}]`,
	}
	for label, raw := range cases {
		if _, err := parseCustomProviders(raw); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}
