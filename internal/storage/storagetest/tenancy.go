package storagetest

import (
	"context"
	"testing"
	"time"

	"github.com/danilovid/aperture/internal/inspector"
	"github.com/danilovid/aperture/internal/limits"
	"github.com/danilovid/aperture/internal/storage"
)

// Two organizations on one gateway must be invisible to each other. That is a
// property of every store, not of the code that calls them, so it is proved
// here once and run against both implementations: a rule PostgreSQL enforces
// and memory does not is a rule the server tests would never notice.

// Fixed ids rather than generated ones: PostgreSQL wants UUIDs, and two
// literals read better in a failure message than two variables.
const (
	OrgA = "11111111-1111-1111-1111-111111111111"
	OrgB = "22222222-2222-2222-2222-222222222222"
)

// RunDLPStoreTenancy proves one organization's incidents stay its own.
// newStore is called per subtest, with the two organizations already known to
// the database where that matters.
func RunDLPStoreTenancy(t *testing.T, newStore func(t *testing.T) storage.DLPStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("the feed shows only the caller's events", func(t *testing.T) {
		s := newStore(t)
		insertEvent(t, s, OrgA, "email", "blocked")
		insertEvent(t, s, OrgB, "aws-key", "blocked")

		a, err := s.List(ctx, OrgA, storage.DLPFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(a) != 1 || a[0].Rule != "email" {
			t.Fatalf("organization A sees %d events %v, want only its own", len(a), rules(a))
		}
		b, err := s.List(ctx, OrgB, storage.DLPFilter{})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(b) != 1 || b[0].Rule != "aws-key" {
			t.Fatalf("organization B sees %d events %v, want only its own", len(b), rules(b))
		}
	})

	t.Run("a filter cannot reach across organizations", func(t *testing.T) {
		s := newStore(t)
		insertEvent(t, s, OrgB, "aws-key", "blocked")

		// Asking organization A for exactly what B has must still find
		// nothing: the filter narrows inside an organization, it does not
		// widen past it.
		got, err := s.List(ctx, OrgA, storage.DLPFilter{Rule: "aws-key", Action: "blocked"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("a filter reached into another organization: %v", rules(got))
		}
	})

	t.Run("counts and the report stay separate", func(t *testing.T) {
		s := newStore(t)
		insertEvent(t, s, OrgA, "email", "blocked")
		insertEvent(t, s, OrgA, "email", "alerted")
		insertEvent(t, s, OrgB, "aws-key", "blocked")

		since := time.Now().Add(-time.Hour)
		sum, err := s.Summary(ctx, OrgA, since)
		if err != nil {
			t.Fatalf("summary: %v", err)
		}
		if sum.Total != 2 || sum.Blocked != 1 {
			t.Errorf("summary for A = %+v, want 2 events and 1 blocked", sum)
		}

		buckets, err := s.Aggregate(ctx, OrgA, since)
		if err != nil {
			t.Fatalf("aggregate: %v", err)
		}
		for _, b := range buckets {
			if b.Rule != "email" {
				t.Errorf("the report for A included %q, which belongs to B", b.Rule)
			}
		}
	})
}

// RunPolicyStoreTenancy proves policies do not leak, including the default
// policy and a key name two organizations both happen to use.
func RunPolicyStoreTenancy(t *testing.T, newStore func(t *testing.T) storage.PolicyStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("the same key name means different policies", func(t *testing.T) {
		s := newStore(t)
		// "shared" is a key id both organizations use: with a primary key of
		// name alone, the second write would overwrite the first.
		if err := s.SetPolicy(ctx, OrgA, "shared", inspector.Policy{Secrets: inspector.ActionBlock}); err != nil {
			t.Fatalf("set policy for A: %v", err)
		}
		if err := s.SetPolicy(ctx, OrgB, "shared", inspector.Policy{Secrets: inspector.ActionAlert}); err != nil {
			t.Fatalf("set policy for B: %v", err)
		}

		a, ok, err := s.GetPolicy(ctx, OrgA, "shared")
		if err != nil || !ok {
			t.Fatalf("get policy for A: ok=%v err=%v", ok, err)
		}
		if a.Secrets != inspector.ActionBlock {
			t.Errorf("A's policy blocks secrets as %q; B overwrote it", a.Secrets)
		}
		b, ok, err := s.GetPolicy(ctx, OrgB, "shared")
		if err != nil || !ok {
			t.Fatalf("get policy for B: ok=%v err=%v", ok, err)
		}
		if b.Secrets != inspector.ActionAlert {
			t.Errorf("B's policy is %q, want alert", b.Secrets)
		}
	})

	t.Run("one organization's policies are not listed to another", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetPolicy(ctx, OrgB, "b-only", inspector.Policy{Secrets: inspector.ActionBlock}); err != nil {
			t.Fatalf("set policy: %v", err)
		}
		list, err := s.ListPolicies(ctx, OrgA)
		if err != nil {
			t.Fatalf("list policies: %v", err)
		}
		if _, found := list["b-only"]; found {
			t.Errorf("organization A was shown B's policy; it sees %v", keysOf(list))
		}
	})

	t.Run("the default policy is per organization", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetDefaultPolicy(ctx, OrgA, inspector.Policy{Secrets: inspector.ActionBlock}); err != nil {
			t.Fatalf("set default for A: %v", err)
		}
		b, err := s.GetDefaultPolicy(ctx, OrgB)
		if err != nil {
			t.Fatalf("get default for B: %v", err)
		}
		if b.Secrets == inspector.ActionBlock {
			t.Error("A's default policy became B's")
		}
	})

	t.Run("deleting is confined to the caller", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetPolicy(ctx, OrgB, "shared", inspector.Policy{Secrets: inspector.ActionBlock}); err != nil {
			t.Fatalf("set policy: %v", err)
		}
		// A has nothing called "shared"; deleting it must not touch B's.
		_ = s.DeletePolicy(ctx, OrgA, "shared")
		if _, ok, err := s.GetPolicy(ctx, OrgB, "shared"); err != nil || !ok {
			t.Errorf("one organization deleted another's policy: ok=%v err=%v", ok, err)
		}
	})
}

// RunLimitStoreTenancy proves budgets and rate ceilings are per organization.
func RunLimitStoreTenancy(t *testing.T, newStore func(t *testing.T) storage.LimitStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("the same key name means different limits", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetLimits(ctx, OrgA, "shared", limits.Limits{BudgetDailyUSD: 10}); err != nil {
			t.Fatalf("set limits for A: %v", err)
		}
		if err := s.SetLimits(ctx, OrgB, "shared", limits.Limits{BudgetDailyUSD: 99}); err != nil {
			t.Fatalf("set limits for B: %v", err)
		}
		a, ok, err := s.GetLimits(ctx, OrgA, "shared")
		if err != nil || !ok {
			t.Fatalf("get limits for A: ok=%v err=%v", ok, err)
		}
		if a.BudgetDailyUSD != 10 {
			t.Errorf("A's budget is %v; B overwrote it", a.BudgetDailyUSD)
		}
	})

	t.Run("one organization's limits are not listed to another", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetLimits(ctx, OrgB, "b-only", limits.Limits{RequestsPerMinute: 5}); err != nil {
			t.Fatalf("set limits: %v", err)
		}
		list, err := s.ListLimits(ctx, OrgA)
		if err != nil {
			t.Fatalf("list limits: %v", err)
		}
		if _, found := list["b-only"]; found {
			t.Errorf("organization A was shown B's limits")
		}
	})

	t.Run("the default is per organization", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetDefaultLimits(ctx, OrgA, limits.Limits{BudgetDailyUSD: 7}); err != nil {
			t.Fatalf("set default for A: %v", err)
		}
		b, err := s.GetDefaultLimits(ctx, OrgB)
		if err != nil {
			t.Fatalf("get default for B: %v", err)
		}
		if b.BudgetDailyUSD == 7 {
			t.Error("A's default budget became B's")
		}
	})

	t.Run("deleting is confined to the caller", func(t *testing.T) {
		s := newStore(t)
		if err := s.SetLimits(ctx, OrgB, "shared", limits.Limits{BudgetDailyUSD: 3}); err != nil {
			t.Fatalf("set limits: %v", err)
		}
		_ = s.DeleteLimits(ctx, OrgA, "shared")
		if _, ok, err := s.GetLimits(ctx, OrgB, "shared"); err != nil || !ok {
			t.Errorf("one organization deleted another's limits: ok=%v err=%v", ok, err)
		}
	})
}

func insertEvent(t *testing.T, s storage.DLPStore, orgID, rule, action string) {
	t.Helper()
	err := s.Insert(context.Background(), storage.DLPEvent{
		OrgID: orgID, Ts: time.Now(), KeyID: "k", Model: "gpt-4o-mini",
		Provider: "openai", Rule: rule, Group: "secrets", Action: action,
		MaskedSample: "***",
	})
	if err != nil {
		t.Fatalf("insert into %s: %v", orgID, err)
	}
}

func rules(events []storage.DLPEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Rule)
	}
	return out
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
