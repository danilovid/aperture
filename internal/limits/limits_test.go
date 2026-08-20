package limits

import (
	"context"
	"errors"
	"testing"
	"time"
)

func at(s string) time.Time {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return ts
}

func TestNoLimitsAlwaysAllows(t *testing.T) {
	tr := NewTracker(nil)
	tr.AddSpend("k", 999)
	if d := tr.Allow(context.Background(), "k", Limits{}); !d.Allowed {
		t.Errorf("empty limits blocked a request: %+v", d)
	}
}

func TestBudgetBlocksOnceExhausted(t *testing.T) {
	tr := NewTracker(nil)
	l := Limits{BudgetDailyUSD: 1.0}
	ctx := context.Background()

	if d := tr.Allow(ctx, "k", l); !d.Allowed {
		t.Fatal("first request blocked")
	}
	tr.AddSpend("k", 0.75)
	if d := tr.Allow(ctx, "k", l); !d.Allowed {
		t.Fatal("blocked below the budget")
	}
	tr.AddSpend("k", 0.30) // total 1.05 > 1.00

	d := tr.Allow(ctx, "k", l)
	if d.Allowed || d.Reason != ReasonBudget {
		t.Fatalf("budget not enforced: %+v", d)
	}
	if d.Spent < 1.0 || d.Limit != 1.0 || d.RetryAfter <= 0 {
		t.Errorf("decision detail wrong: %+v", d)
	}
	// Другой ключ не задет.
	if d := tr.Allow(ctx, "other", l); !d.Allowed {
		t.Error("budget leaked across keys")
	}
}

func TestBudgetResetsAtUTCMidnight(t *testing.T) {
	tr := NewTracker(nil)
	l := Limits{BudgetDailyUSD: 1.0}
	ctx := context.Background()

	tr.now = func() time.Time { return at("2026-08-20T23:59:00Z") }
	tr.AddSpend("k", 2.0)
	if d := tr.Allow(ctx, "k", l); d.Allowed {
		t.Fatal("over-budget request allowed before midnight")
	}

	tr.now = func() time.Time { return at("2026-08-21T00:00:01Z") }
	if d := tr.Allow(ctx, "k", l); !d.Allowed {
		t.Errorf("budget did not reset at UTC midnight: %+v", d)
	}
	if s := tr.Spent("k"); s != 0 {
		t.Errorf("spend carried over the day boundary: %v", s)
	}
}

func TestRateLimitPerMinuteWindow(t *testing.T) {
	tr := NewTracker(nil)
	l := Limits{RequestsPerMinute: 2}
	ctx := context.Background()

	base := at("2026-08-20T10:00:00Z")
	tr.now = func() time.Time { return base }

	for i := 0; i < 2; i++ {
		if d := tr.Allow(ctx, "k", l); !d.Allowed {
			t.Fatalf("request %d blocked inside the limit", i+1)
		}
	}
	d := tr.Allow(ctx, "k", l)
	if d.Allowed || d.Reason != ReasonRate {
		t.Fatalf("rate limit not enforced: %+v", d)
	}
	if d.RetryAfter <= 0 || d.RetryAfter > 61 {
		t.Errorf("implausible RetryAfter: %d", d.RetryAfter)
	}

	// The next window lets traffic through again.
	tr.now = func() time.Time { return base.Add(61 * time.Second) }
	if d := tr.Allow(ctx, "k", l); !d.Allowed {
		t.Errorf("window did not roll over: %+v", d)
	}
}

// A restart must not hand a key a fresh budget: today's spend is recovered
// from the request log on first use.
func TestBudgetSeededFromStorage(t *testing.T) {
	var seedCalls int
	seed := func(_ context.Context, keyID string, since time.Time) (float64, error) {
		seedCalls++
		if keyID != "k" {
			t.Errorf("seeded wrong key: %s", keyID)
		}
		if since.Hour() != 0 || since.Minute() != 0 {
			t.Errorf("seed window is not the start of the UTC day: %v", since)
		}
		return 0.9, nil
	}
	tr := NewTracker(seed)
	tr.now = func() time.Time { return at("2026-08-20T12:00:00Z") }
	l := Limits{BudgetDailyUSD: 1.0}
	ctx := context.Background()

	if d := tr.Allow(ctx, "k", l); !d.Allowed || d.Spent != 0.9 {
		t.Fatalf("seeded spend not applied: %+v", d)
	}
	tr.AddSpend("k", 0.2) // 1.1 > 1.0
	if d := tr.Allow(ctx, "k", l); d.Allowed {
		t.Fatal("budget not enforced after seeding")
	}
	if seedCalls != 1 {
		t.Errorf("storage seeded %d times, want once per key per day", seedCalls)
	}
}

// If the request log cannot be read, traffic keeps flowing rather than being
// blocked by an infrastructure problem.
func TestSeedFailureDoesNotBlockTraffic(t *testing.T) {
	tr := NewTracker(func(context.Context, string, time.Time) (float64, error) {
		return 0, errors.New("db down")
	})
	if d := tr.Allow(context.Background(), "k", Limits{BudgetDailyUSD: 1}); !d.Allowed {
		t.Errorf("seed failure blocked traffic: %+v", d)
	}
}

func TestMarkNotifiedFiresOncePerDay(t *testing.T) {
	tr := NewTracker(nil)
	tr.now = func() time.Time { return at("2026-08-20T10:00:00Z") }
	if !tr.MarkNotified("k") {
		t.Fatal("first alert suppressed")
	}
	if tr.MarkNotified("k") {
		t.Error("alert repeated within the same day")
	}
	tr.now = func() time.Time { return at("2026-08-21T10:00:00Z") }
	if !tr.MarkNotified("k") {
		t.Error("alert not re-armed on the next day")
	}
}
