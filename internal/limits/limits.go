// Package limits enforces per-key spending budgets and request rate limits.
//
// Counters live in this process: a fixed one-minute window for the rate limit
// and a running daily total for the budget. The daily total is seeded from the
// request log on first use, so a restart does not hand a key a fresh budget.
// In a multi-instance deployment each instance counts its own traffic.
package limits

import (
	"context"
	"sync"
	"time"
)

// Limits are the per-key ceilings. A zero value means "no limit".
type Limits struct {
	// BudgetDailyUSD caps spend per UTC day.
	BudgetDailyUSD float64 `json:"budget_daily_usd,omitempty"`
	// RequestsPerMinute caps request rate.
	RequestsPerMinute int `json:"requests_per_minute,omitempty"`
}

// Empty reports whether nothing is limited.
func (l Limits) Empty() bool { return l.BudgetDailyUSD <= 0 && l.RequestsPerMinute <= 0 }

// Reason says which ceiling stopped a request.
type Reason string

const (
	ReasonNone   Reason = ""
	ReasonBudget Reason = "budget"
	ReasonRate   Reason = "rate"
)

// Decision is the outcome of a limit check.
type Decision struct {
	Allowed bool
	Reason  Reason
	// Spent and Limit describe the budget at decision time (USD).
	Spent float64
	Limit float64
	// RetryAfter is a hint in seconds for the client.
	RetryAfter int
}

// SpendSeeder returns how much a key has already spent since a moment. It lets
// the tracker recover the day's total after a restart; nil disables seeding.
// The organization is part of the question because the request log it reads is
// scoped by organization: asking without one would read nothing, or worse,
// everything.
type SpendSeeder func(ctx context.Context, orgID, keyID string, since time.Time) (float64, error)

type keyState struct {
	day      string  // UTC date the spend belongs to
	spent    float64 // USD spent today
	seeded   bool    // day total already recovered from storage
	winStart time.Time
	winCount int
	notified bool // budget-exhausted alert already emitted today
}

// Tracker holds per-key counters. Safe for concurrent use.
type Tracker struct {
	mu   sync.Mutex
	keys map[string]*keyState
	seed SpendSeeder
	now  func() time.Time // injectable for tests
}

// counterKey identifies one organization's key. Key ids are unique on their
// own, but composing the counter key means a budget can never be shared
// across organizations by accident — including by a future id scheme that
// only promises uniqueness within an organization.
func counterKey(orgID, keyID string) string { return orgID + "|" + keyID }

// NewTracker creates a tracker. seed may be nil.
func NewTracker(seed SpendSeeder) *Tracker {
	return &Tracker{keys: map[string]*keyState{}, seed: seed, now: time.Now}
}

func (t *Tracker) state(now time.Time, orgID, keyID string) *keyState {
	ck := counterKey(orgID, keyID)
	st := t.keys[ck]
	if st == nil {
		st = &keyState{}
		t.keys[ck] = st
	}
	// Roll over at UTC midnight.
	if day := now.UTC().Format("2006-01-02"); st.day != day {
		st.day, st.spent, st.seeded, st.notified = day, 0, false, false
	}
	return st
}

// Allow checks a request against the key's limits before it is proxied.
func (t *Tracker) Allow(ctx context.Context, orgID, keyID string, l Limits) Decision {
	if l.Empty() {
		return Decision{Allowed: true}
	}
	now := t.now()

	t.mu.Lock()
	st := t.state(now, orgID, keyID)
	needSeed := l.BudgetDailyUSD > 0 && !st.seeded && t.seed != nil
	t.mu.Unlock()

	// Seeding touches storage, so it runs without the lock held.
	if needSeed {
		startOfDay := now.UTC().Truncate(24 * time.Hour)
		if spent, err := t.seed(ctx, orgID, keyID, startOfDay); err == nil {
			t.mu.Lock()
			st = t.state(now, orgID, keyID)
			if !st.seeded {
				st.spent += spent
				st.seeded = true
			}
			t.mu.Unlock()
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	st = t.state(now, orgID, keyID)
	st.seeded = st.seeded || t.seed == nil

	if l.BudgetDailyUSD > 0 && st.spent >= l.BudgetDailyUSD {
		// Seconds left until the budget resets at UTC midnight.
		reset := now.UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
		return Decision{
			Reason: ReasonBudget, Spent: st.spent, Limit: l.BudgetDailyUSD,
			RetryAfter: int(reset.Sub(now.UTC()).Seconds()) + 1,
		}
	}

	if l.RequestsPerMinute > 0 {
		if now.Sub(st.winStart) >= time.Minute {
			st.winStart, st.winCount = now, 0
		}
		if st.winCount >= l.RequestsPerMinute {
			return Decision{
				Reason: ReasonRate, Spent: st.spent, Limit: l.BudgetDailyUSD,
				RetryAfter: int(time.Minute.Seconds()-now.Sub(st.winStart).Seconds()) + 1,
			}
		}
		st.winCount++
	}

	return Decision{Allowed: true, Spent: st.spent, Limit: l.BudgetDailyUSD}
}

// AddSpend records the cost of a completed request.
func (t *Tracker) AddSpend(orgID, keyID string, usd float64) {
	if usd <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state(t.now(), orgID, keyID).spent += usd
}

// Spent returns today's spend for a key.
func (t *Tracker) Spent(orgID, keyID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state(t.now(), orgID, keyID).spent
}

// MarkNotified reports whether a budget-exhausted alert still needs sending
// today, flipping the flag so the alert fires once per key per day.
func (t *Tracker) MarkNotified(orgID, keyID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.state(t.now(), orgID, keyID)
	if st.notified {
		return false
	}
	st.notified = true
	return true
}
