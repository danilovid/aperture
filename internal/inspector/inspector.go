// Package inspector scans outbound LLM traffic for secrets, PII and custom
// stop-patterns before it leaves the network.
package inspector

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Action is what the gateway does when a rule matches.
type Action string

const (
	ActionOff    Action = "off"    // detector disabled
	ActionAlert  Action = "alert"  // pass through, record an event
	ActionRedact Action = "redact" // mask the match, record an event
	ActionBlock  Action = "block"  // reject the request, record an event
)

// severity orders actions so the strictest finding wins for the verdict.
func (a Action) severity() int {
	switch a {
	case ActionBlock:
		return 3
	case ActionRedact:
		return 2
	case ActionAlert:
		return 1
	default:
		return 0
	}
}

// ValidAction reports whether s is a recognized action value.
func ValidAction(s string) bool {
	switch Action(s) {
	case ActionOff, ActionAlert, ActionRedact, ActionBlock:
		return true
	}
	return false
}

// Group is a class of detectors sharing one configured action.
type Group string

const (
	GroupSecrets Group = "secrets"
	GroupPII     Group = "pii"
	GroupCustom  Group = "custom"
)

// Policy maps detector groups to actions and carries user-defined rules.
type Policy struct {
	Secrets     Action       `json:"secrets"`
	PII         Action       `json:"pii"`
	Custom      Action       `json:"custom"`
	CustomRules []CustomRule `json:"custom_rules,omitempty"`
}

// CustomRule is a user-supplied pattern (regex or literal stop-word).
type CustomRule struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// DefaultPolicy blocks secrets and redacts PII.
func DefaultPolicy() Policy {
	return Policy{Secrets: ActionBlock, PII: ActionRedact, Custom: ActionAlert}
}

// Finding is one rule match inside a scanned text.
type Finding struct {
	Rule         string `json:"rule"`
	Group        Group  `json:"group"`
	Action       Action `json:"action"`
	Start        int    `json:"-"`
	End          int    `json:"-"`
	MaskedSample string `json:"masked_sample"`
}

type rule struct {
	name     string
	group    Group
	re       *regexp.Regexp
	validate func(match string) bool // optional extra check (e.g. Luhn)
}

// Inspector holds the compiled built-in ruleset. Safe for concurrent use.
type Inspector struct {
	rules []rule
}

// New compiles the built-in detectors.
func New() *Inspector {
	return &Inspector{rules: builtinRules()}
}

func builtinRules() []rule {
	return []rule{
		// ── Secrets ──────────────────────────────────────────────────────
		{name: "aws-access-key", group: GroupSecrets,
			re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
		{name: "github-token", group: GroupSecrets,
			re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})\b`)},
		{name: "gitlab-token", group: GroupSecrets,
			re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}\b`)},
		{name: "slack-token", group: GroupSecrets,
			re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
		{name: "openai-key", group: GroupSecrets,
			re: regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{20,}\b`)},
		{name: "private-key", group: GroupSecrets,
			re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
		{name: "jwt", group: GroupSecrets,
			re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`)},
		{name: "generic-credential", group: GroupSecrets,
			re: regexp.MustCompile(`(?i)\b(?:api[_-]?key|secret[_-]?key|access[_-]?token|password)\b\s*[:=]\s*['"]?[A-Za-z0-9_\-/+]{16,}`)},

		// ── PII ──────────────────────────────────────────────────────────
		{name: "email", group: GroupPII,
			re: regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)},
		{name: "credit-card", group: GroupPII,
			re:       regexp.MustCompile(`\b(?:\d[ \-]?){13,19}\b`),
			validate: luhnValid},
		{name: "phone", group: GroupPII,
			re: regexp.MustCompile(`\+\d[\d \-]{7,14}\d\b`)},
		{name: "iban", group: GroupPII,
			re:       regexp.MustCompile(`\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b`),
			validate: ibanValid},
	}
}

// Scan runs every enabled detector over text and returns findings sorted by
// position. The policy decides each finding's action.
func (i *Inspector) Scan(text string, p Policy) []Finding {
	var out []Finding

	actionFor := func(g Group) Action {
		switch g {
		case GroupSecrets:
			return p.Secrets
		case GroupPII:
			return p.PII
		default:
			return p.Custom
		}
	}

	for _, r := range i.rules {
		action := actionFor(r.group)
		if action == ActionOff || action == "" {
			continue
		}
		for _, loc := range r.re.FindAllStringIndex(text, -1) {
			match := text[loc[0]:loc[1]]
			if r.validate != nil && !r.validate(match) {
				continue
			}
			out = append(out, Finding{
				Rule:         r.name,
				Group:        r.group,
				Action:       action,
				Start:        loc[0],
				End:          loc[1],
				MaskedSample: MaskSample(match),
			})
		}
	}

	if p.Custom != ActionOff && p.Custom != "" {
		for _, cr := range p.CustomRules {
			re, err := regexp.Compile("(?i)" + cr.Pattern)
			if err != nil {
				continue // invalid user pattern; validated on save in the admin API
			}
			for _, loc := range re.FindAllStringIndex(text, -1) {
				out = append(out, Finding{
					Rule:         "custom:" + cr.Name,
					Group:        GroupCustom,
					Action:       p.Custom,
					Start:        loc[0],
					End:          loc[1],
					MaskedSample: MaskSample(text[loc[0]:loc[1]]),
				})
			}
		}
	}

	sort.Slice(out, func(a, b int) bool { return out[a].Start < out[b].Start })
	return out
}

// Verdict returns the strictest action across findings (off when none).
func Verdict(findings []Finding) Action {
	v := ActionOff
	for _, f := range findings {
		if f.Action.severity() > v.severity() {
			v = f.Action
		}
	}
	return v
}

// Redact replaces every finding whose action is redact or block with a
// placeholder. Overlapping spans are merged.
func Redact(text string, findings []Finding) string {
	spans := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if f.Action == ActionRedact || f.Action == ActionBlock {
			spans = append(spans, f)
		}
	}
	if len(spans) == 0 {
		return text
	}
	sort.Slice(spans, func(a, b int) bool { return spans[a].Start < spans[b].Start })

	var b strings.Builder
	pos := 0
	for _, f := range spans {
		if f.Start < pos {
			continue // overlaps a span already replaced
		}
		b.WriteString(text[pos:f.Start])
		b.WriteString(fmt.Sprintf("[REDACTED:%s]", f.Rule))
		pos = f.End
	}
	b.WriteString(text[pos:])
	return b.String()
}

// MaskSample keeps the first 4 characters and masks the rest, capped at 24
// characters total. Recorded events must never contain the raw match.
func MaskSample(match string) string {
	const keep, maxLen = 4, 24
	if len(match) <= keep {
		return strings.Repeat("*", len(match))
	}
	masked := len(match) - keep
	if keep+masked > maxLen {
		masked = maxLen - keep
	}
	return match[:keep] + strings.Repeat("*", masked)
}

// luhnValid checks the card-number checksum over the digits in match.
func luhnValid(match string) bool {
	var digits []int
	for _, c := range match {
		if c >= '0' && c <= '9' {
			digits = append(digits, int(c-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := digits[i]
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// ibanValid checks the mod-97 checksum (ISO 13616).
func ibanValid(match string) bool {
	if len(match) < 15 || len(match) > 34 {
		return false
	}
	rearranged := match[4:] + match[:4]
	rem := 0
	for _, c := range rearranged {
		var v int
		switch {
		case c >= '0' && c <= '9':
			v = int(c - '0')
		case c >= 'A' && c <= 'Z':
			v = int(c-'A') + 10
		default:
			return false
		}
		if v > 9 {
			rem = (rem*100 + v) % 97
		} else {
			rem = (rem*10 + v) % 97
		}
	}
	return rem == 1
}
