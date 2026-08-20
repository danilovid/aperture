// Package report turns the DLP event log into the answer teams need before
// they dare switch a detector to block: what would have been stopped last
// week, for which rules, on which keys.
package report

import (
	"sort"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

// PolicyActions is the group→action part of a policy, which is all the report
// needs to explain why an event passed.
type PolicyActions struct {
	Secrets string `json:"secrets"`
	PII     string `json:"pii"`
	Custom  string `json:"custom"`
}

func actionsOf(p inspector.Policy) PolicyActions {
	return PolicyActions{
		Secrets: string(p.ActionFor(inspector.GroupSecrets)),
		PII:     string(p.ActionFor(inspector.GroupPII)),
		Custom:  string(p.ActionFor(inspector.GroupCustom)),
	}
}

// GroupImpact answers "what changes if this group is switched to block".
type GroupImpact struct {
	// Events is every match in the group over the period.
	Events int64 `json:"events"`
	// WouldBlock is what switching to block newly stops: matches that are not
	// blocked today and are not silenced by a mute or an allowlist entry.
	WouldBlock int64 `json:"would_block"`
	// Keys is how many aperture keys those blocks land on.
	Keys int `json:"keys"`
}

// RuleStat is one detector's activity over the period.
type RuleStat struct {
	Rule       string    `json:"rule"`
	Group      string    `json:"group"`
	Total      int64     `json:"total"`
	Blocked    int64     `json:"blocked"`
	Redacted   int64     `json:"redacted"`
	Alerted    int64     `json:"alerted"`
	Suppressed int64     `json:"suppressed"`
	WouldBlock int64     `json:"would_block"`
	Keys       int       `json:"keys"`
	First      time.Time `json:"first_seen"`
	Last       time.Time `json:"last_seen"`
	Sample     string    `json:"masked_sample"`
}

// KeyStat is one aperture key's activity, with the policy that let it through.
type KeyStat struct {
	KeyID      string        `json:"key_id"`
	Total      int64         `json:"total"`
	Blocked    int64         `json:"blocked"`
	WouldBlock int64         `json:"would_block"`
	TopRule    string        `json:"top_rule"`
	Policy     PolicyActions `json:"policy"`
}

// AgentStat is the same view per agent, for keys shared by several agents.
type AgentStat struct {
	Agent      string `json:"agent"`
	Total      int64  `json:"total"`
	WouldBlock int64  `json:"would_block"`
	TopRule    string `json:"top_rule"`
}

// Report is the audit answer for one period.
type Report struct {
	Period string    `json:"period"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	// Totals counts every recorded event in the period.
	Totals storage.DLPSummary `json:"totals"`
	// WouldBlock is the headline: per group, and summed across all three.
	WouldBlock struct {
		Groups map[string]GroupImpact `json:"groups"`
		Total  int64                  `json:"total"`
		Keys   int                    `json:"keys"`
	} `json:"would_block"`
	DefaultPolicy PolicyActions `json:"default_policy"`
	Rules         []RuleStat    `json:"rules"`
	Keys          []KeyStat     `json:"keys"`
	Agents        []AgentStat   `json:"agents"`
	// Truncated marks a period with more distinct buckets than the store
	// returns, so the tables understate the tail. Totals stay exact.
	Truncated bool `json:"truncated,omitempty"`
}

// Options carries what Build cannot derive from the buckets themselves.
type Options struct {
	Period string
	Since  time.Time
	Until  time.Time
	Totals storage.DLPSummary
	// PolicyFor resolves the effective policy for a key; nil leaves the
	// per-key policy columns empty.
	PolicyFor func(keyID string) inspector.Policy
	// DefaultPolicy is what keys without their own binding get.
	DefaultPolicy inspector.Policy
	Truncated     bool
}

// wouldBlock reports whether flipping this bucket's group to block would newly
// stop these requests. Already-blocked traffic is no change; suppressed matches
// stay suppressed, because a mute or allowlist entry is a deliberate choice.
func wouldBlock(action string) bool {
	return action != "blocked" && action != "suppressed"
}

// Build folds store-side aggregates into the report.
func Build(buckets []storage.DLPBucket, o Options) Report {
	rep := Report{
		Period:        o.Period,
		Since:         o.Since,
		Until:         o.Until,
		Totals:        o.Totals,
		DefaultPolicy: actionsOf(o.DefaultPolicy),
		Truncated:     o.Truncated,
		Rules:         []RuleStat{},
		Keys:          []KeyStat{},
		Agents:        []AgentStat{},
	}
	rep.WouldBlock.Groups = map[string]GroupImpact{}

	rules := map[string]*RuleStat{}
	ruleKeys := map[string]map[string]bool{}
	keys := map[string]*KeyStat{}
	keyRules := map[string]map[string]int64{}
	agents := map[string]*AgentStat{}
	agentRules := map[string]map[string]int64{}
	groupKeys := map[string]map[string]bool{}
	blockedKeys := map[string]bool{}

	for _, b := range buckets {
		blocks := wouldBlock(b.Action)

		r := rules[b.Rule]
		if r == nil {
			r = &RuleStat{Rule: b.Rule, Group: b.Group, First: b.First, Last: b.Last, Sample: b.Sample}
			rules[b.Rule] = r
			ruleKeys[b.Rule] = map[string]bool{}
		}
		r.Total += b.Count
		switch b.Action {
		case "blocked":
			r.Blocked += b.Count
		case "redacted":
			r.Redacted += b.Count
		case "alerted":
			r.Alerted += b.Count
		case "suppressed":
			r.Suppressed += b.Count
		}
		if blocks {
			r.WouldBlock += b.Count
		}
		if b.First.Before(r.First) {
			r.First = b.First
		}
		if b.Last.After(r.Last) {
			r.Last = b.Last
		}
		if r.Sample == "" {
			r.Sample = b.Sample
		}
		ruleKeys[b.Rule][b.KeyID] = true

		g := rep.WouldBlock.Groups[b.Group]
		g.Events += b.Count
		if blocks {
			g.WouldBlock += b.Count
			rep.WouldBlock.Total += b.Count
			blockedKeys[b.KeyID] = true
			if groupKeys[b.Group] == nil {
				groupKeys[b.Group] = map[string]bool{}
			}
			groupKeys[b.Group][b.KeyID] = true
		}
		rep.WouldBlock.Groups[b.Group] = g

		k := keys[b.KeyID]
		if k == nil {
			k = &KeyStat{KeyID: b.KeyID}
			if o.PolicyFor != nil {
				k.Policy = actionsOf(o.PolicyFor(b.KeyID))
			}
			keys[b.KeyID] = k
			keyRules[b.KeyID] = map[string]int64{}
		}
		k.Total += b.Count
		if b.Action == "blocked" {
			k.Blocked += b.Count
		}
		if blocks {
			k.WouldBlock += b.Count
		}
		keyRules[b.KeyID][b.Rule] += b.Count

		if b.Agent != "" {
			a := agents[b.Agent]
			if a == nil {
				a = &AgentStat{Agent: b.Agent}
				agents[b.Agent] = a
				agentRules[b.Agent] = map[string]int64{}
			}
			a.Total += b.Count
			if blocks {
				a.WouldBlock += b.Count
			}
			agentRules[b.Agent][b.Rule] += b.Count
		}
	}

	for group, impact := range rep.WouldBlock.Groups {
		impact.Keys = len(groupKeys[group])
		rep.WouldBlock.Groups[group] = impact
	}
	rep.WouldBlock.Keys = len(blockedKeys)

	for name, r := range rules {
		r.Keys = len(ruleKeys[name])
		rep.Rules = append(rep.Rules, *r)
	}
	sort.Slice(rep.Rules, func(i, j int) bool {
		if rep.Rules[i].WouldBlock != rep.Rules[j].WouldBlock {
			return rep.Rules[i].WouldBlock > rep.Rules[j].WouldBlock
		}
		if rep.Rules[i].Total != rep.Rules[j].Total {
			return rep.Rules[i].Total > rep.Rules[j].Total
		}
		return rep.Rules[i].Rule < rep.Rules[j].Rule
	})

	for id, k := range keys {
		k.TopRule = topOf(keyRules[id])
		rep.Keys = append(rep.Keys, *k)
	}
	sort.Slice(rep.Keys, func(i, j int) bool {
		if rep.Keys[i].WouldBlock != rep.Keys[j].WouldBlock {
			return rep.Keys[i].WouldBlock > rep.Keys[j].WouldBlock
		}
		if rep.Keys[i].Total != rep.Keys[j].Total {
			return rep.Keys[i].Total > rep.Keys[j].Total
		}
		return rep.Keys[i].KeyID < rep.Keys[j].KeyID
	})

	for name, a := range agents {
		a.TopRule = topOf(agentRules[name])
		rep.Agents = append(rep.Agents, *a)
	}
	sort.Slice(rep.Agents, func(i, j int) bool {
		if rep.Agents[i].WouldBlock != rep.Agents[j].WouldBlock {
			return rep.Agents[i].WouldBlock > rep.Agents[j].WouldBlock
		}
		if rep.Agents[i].Total != rep.Agents[j].Total {
			return rep.Agents[i].Total > rep.Agents[j].Total
		}
		return rep.Agents[i].Agent < rep.Agents[j].Agent
	})

	return rep
}

// topOf returns the highest-count name, ties broken alphabetically so the
// report is stable across runs.
func topOf(counts map[string]int64) string {
	best, bestN := "", int64(-1)
	for name, n := range counts {
		if n > bestN || (n == bestN && name < best) {
			best, bestN = name, n
		}
	}
	return best
}
