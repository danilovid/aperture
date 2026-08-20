package report

import (
	"testing"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/storage"
)

func bucket(rule, group, key, agent, action string, n int64) storage.DLPBucket {
	ts := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	return storage.DLPBucket{
		Rule: rule, Group: group, KeyID: key, Agent: agent, Action: action,
		Count: n, First: ts, Last: ts.Add(time.Hour), Sample: "AKIA****",
	}
}

func alertMode() Options {
	p := inspector.Policy{Secrets: inspector.ActionAlert, PII: inspector.ActionRedact, Custom: inspector.ActionOff}
	return Options{
		Period:        "7d",
		Totals:        storage.DLPSummary{Total: 100},
		DefaultPolicy: p,
		PolicyFor:     func(string) inspector.Policy { return p },
	}
}

// The headline number: alert-mode traffic is what flipping the switch stops.
func TestWouldBlockCountsOnlyNewBlocks(t *testing.T) {
	rep := Build([]storage.DLPBucket{
		bucket("aws-access-key", "secrets", "k1", "ci-bot", "alerted", 40),
		bucket("aws-access-key", "secrets", "k2", "", "blocked", 5),        // already blocked
		bucket("github-token", "secrets", "k1", "ci-bot", "suppressed", 9), // muted on purpose
		bucket("email", "pii", "k3", "", "redacted", 7),
	}, alertMode())

	secrets := rep.WouldBlock.Groups["secrets"]
	if secrets.WouldBlock != 40 {
		t.Errorf("secrets would_block = %d, want 40 (blocked and suppressed excluded)", secrets.WouldBlock)
	}
	if secrets.Events != 54 {
		t.Errorf("secrets events = %d, want 54", secrets.Events)
	}
	if secrets.Keys != 1 {
		t.Errorf("secrets keys = %d, want 1 (only k1 gains blocks)", secrets.Keys)
	}
	if pii := rep.WouldBlock.Groups["pii"]; pii.WouldBlock != 7 {
		t.Errorf("pii would_block = %d, want 7 (redacted traffic would be rejected instead)", pii.WouldBlock)
	}
	if rep.WouldBlock.Total != 47 {
		t.Errorf("total would_block = %d, want 47", rep.WouldBlock.Total)
	}
	if rep.WouldBlock.Keys != 2 {
		t.Errorf("keys affected = %d, want 2", rep.WouldBlock.Keys)
	}
}

func TestRuleAndKeyBreakdown(t *testing.T) {
	rep := Build([]storage.DLPBucket{
		bucket("aws-access-key", "secrets", "k1", "ci-bot", "alerted", 12),
		bucket("aws-access-key", "secrets", "k2", "qa-bot", "alerted", 3),
		bucket("aws-access-key", "secrets", "k1", "ci-bot", "suppressed", 4),
		bucket("email", "pii", "k1", "ci-bot", "redacted", 30),
	}, alertMode())

	// Sorted by would_block, so the rule to act on first is at the top.
	if rep.Rules[0].Rule != "email" {
		t.Errorf("top rule = %q, want email (30 would-blocks)", rep.Rules[0].Rule)
	}
	aws := rep.Rules[1]
	if aws.Total != 19 || aws.Alerted != 15 || aws.Suppressed != 4 || aws.WouldBlock != 15 {
		t.Errorf("aws-access-key stats = %+v", aws)
	}
	if aws.Keys != 2 {
		t.Errorf("aws-access-key keys = %d, want 2", aws.Keys)
	}

	if rep.Keys[0].KeyID != "k1" || rep.Keys[0].WouldBlock != 42 {
		t.Errorf("top key = %+v, want k1 with 42", rep.Keys[0])
	}
	if rep.Keys[0].TopRule != "email" {
		t.Errorf("k1 top rule = %q, want email", rep.Keys[0].TopRule)
	}
	if rep.Keys[0].Policy.Secrets != "alert" {
		t.Errorf("policy not attached to the key: %+v", rep.Keys[0].Policy)
	}
	if rep.Agents[0].Agent != "ci-bot" || rep.Agents[0].WouldBlock != 42 {
		t.Errorf("top agent = %+v, want ci-bot with 42", rep.Agents[0])
	}
}

// A quiet week must still render, not produce nulls the console has to guard.
func TestEmptyReportIsWellFormed(t *testing.T) {
	rep := Build(nil, alertMode())
	if rep.Rules == nil || rep.Keys == nil || rep.Agents == nil {
		t.Error("empty slices must be non-nil so the JSON has [] not null")
	}
	if rep.WouldBlock.Total != 0 || len(rep.WouldBlock.Groups) != 0 {
		t.Errorf("empty report claims impact: %+v", rep.WouldBlock)
	}
	if rep.DefaultPolicy.Secrets != "alert" {
		t.Errorf("default policy = %+v", rep.DefaultPolicy)
	}
}

// Events without an agent header must not create a phantom "" agent row.
func TestAgentlessTrafficIsNotAnAgent(t *testing.T) {
	rep := Build([]storage.DLPBucket{
		bucket("aws-access-key", "secrets", "k1", "", "alerted", 5),
	}, alertMode())
	if len(rep.Agents) != 0 {
		t.Errorf("agents = %+v, want none", rep.Agents)
	}
}
