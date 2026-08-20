package storage

import (
	"context"
	"sort"
	"sync"
	"time"
)

// DLPEvent records one rule match on outbound traffic. MaskedSample must
// never contain the raw matched content.
type DLPEvent struct {
	ID           int64     `json:"id"`
	Ts           time.Time `json:"ts"`
	KeyID        string    `json:"key_id"`
	Model        string    `json:"model"`
	Provider     string    `json:"provider"`
	Rule         string    `json:"rule"`
	Group        string    `json:"group"`
	Action       string    `json:"action"` // blocked | redacted | alerted | suppressed
	MaskedSample string    `json:"masked_sample"`
	// Agent and Session identify which agent run produced this, from the
	// X-Aperture-Agent / X-Aperture-Session request headers.
	Agent   string `json:"agent,omitempty"`
	Session string `json:"session,omitempty"`
}

// DLPFilter narrows DLPStore.List results. Zero values mean "any".
type DLPFilter struct {
	Action  string
	Rule    string
	KeyID   string
	Agent   string
	Session string
	Since   time.Time
	Limit   int
}

// DLPSummary aggregates events for dashboard KPIs.
type DLPSummary struct {
	Total    int64 `json:"total"`
	Blocked  int64 `json:"blocked"`
	Redacted int64 `json:"redacted"`
	Alerted  int64 `json:"alerted"`
	// Suppressed counts matches held back by a mute or allowlist entry, so
	// silencing a detector is visible rather than invisible.
	Suppressed int64 `json:"suppressed"`
}

// DLPBucket is one aggregated slice of the event log, grouped by everything
// the audit report needs to answer "what changes if we enable block".
// Aggregating in the store keeps a 7-day report from streaming every row.
type DLPBucket struct {
	Rule   string
	Group  string
	KeyID  string
	Agent  string
	Action string
	Count  int64
	First  time.Time
	Last   time.Time
	Sample string // one masked sample, for context in the report
}

// MaxDLPBuckets caps how many buckets a store returns. Agents are free-form
// strings, so a misbehaving client could otherwise blow up the group count.
const MaxDLPBuckets = 5000

// DLPStore persists and queries DLP events.
type DLPStore interface {
	Insert(ctx context.Context, e DLPEvent) error
	List(ctx context.Context, f DLPFilter) ([]DLPEvent, error)
	Summary(ctx context.Context, since time.Time) (DLPSummary, error)
	// Aggregate returns per-(rule, key, agent, action) counts since a point in
	// time, largest first, capped at MaxDLPBuckets.
	Aggregate(ctx context.Context, since time.Time) ([]DLPBucket, error)
}

// MemDLPStore is a fixed-size in-memory ring buffer of recent events.
// It backs DLP visibility in no-DB mode and is safe for concurrent use.
type MemDLPStore struct {
	mu     sync.RWMutex
	events []DLPEvent // ring; next points at the oldest slot once full
	next   int
	total  int64
}

// NewMemDLPStore keeps the most recent `size` events.
func NewMemDLPStore(size int) *MemDLPStore {
	if size <= 0 {
		size = 1000
	}
	return &MemDLPStore{events: make([]DLPEvent, 0, size)}
}

var _ DLPStore = (*MemDLPStore)(nil)

func (s *MemDLPStore) Insert(_ context.Context, e DLPEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total++
	e.ID = s.total
	if e.Ts.IsZero() {
		e.Ts = time.Now()
	}
	if len(s.events) < cap(s.events) {
		s.events = append(s.events, e)
		return nil
	}
	s.events[s.next] = e
	s.next = (s.next + 1) % cap(s.events)
	return nil
}

// List returns events newest-first, applying the filter.
func (s *MemDLPStore) List(_ context.Context, f DLPFilter) ([]DLPEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	limit := f.Limit
	if limit <= 0 || limit > len(s.events) {
		limit = len(s.events)
	}

	out := make([]DLPEvent, 0, limit)
	n := len(s.events)
	for i := 0; i < n && len(out) < limit; i++ {
		// Walk backwards from the newest slot.
		idx := (s.next - 1 - i + 2*n) % n
		e := s.events[idx]
		if f.Action != "" && e.Action != f.Action {
			continue
		}
		if f.Rule != "" && e.Rule != f.Rule {
			continue
		}
		if f.KeyID != "" && e.KeyID != f.KeyID {
			continue
		}
		if f.Agent != "" && e.Agent != f.Agent {
			continue
		}
		if f.Session != "" && e.Session != f.Session {
			continue
		}
		if !f.Since.IsZero() && e.Ts.Before(f.Since) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *MemDLPStore) Summary(_ context.Context, since time.Time) (DLPSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sum DLPSummary
	for _, e := range s.events {
		if !since.IsZero() && e.Ts.Before(since) {
			continue
		}
		sum.Total++
		switch e.Action {
		case "blocked":
			sum.Blocked++
		case "redacted":
			sum.Redacted++
		case "alerted":
			sum.Alerted++
		case "suppressed":
			sum.Suppressed++
		}
	}
	return sum, nil
}

// Aggregate groups the retained events for the audit report.
func (s *MemDLPStore) Aggregate(_ context.Context, since time.Time) ([]DLPBucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type key struct{ rule, group, keyID, agent, action string }
	byKey := make(map[key]*DLPBucket)
	for _, e := range s.events {
		if !since.IsZero() && e.Ts.Before(since) {
			continue
		}
		k := key{e.Rule, e.Group, e.KeyID, e.Agent, e.Action}
		b := byKey[k]
		if b == nil {
			b = &DLPBucket{
				Rule: e.Rule, Group: e.Group, KeyID: e.KeyID, Agent: e.Agent, Action: e.Action,
				First: e.Ts, Last: e.Ts, Sample: e.MaskedSample,
			}
			byKey[k] = b
		}
		b.Count++
		if e.Ts.Before(b.First) {
			b.First = e.Ts
		}
		if e.Ts.After(b.Last) {
			b.Last = e.Ts
		}
	}

	out := make([]DLPBucket, 0, len(byKey))
	for _, b := range byKey {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Rule < out[j].Rule
	})
	if len(out) > MaxDLPBuckets {
		out = out[:MaxDLPBuckets]
	}
	return out, nil
}
