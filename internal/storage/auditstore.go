package storage

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Who can change an organization, as the audit log names them.
const (
	ActorUser     = "user"     // a person, signed in to the console
	ActorToken    = "token"    // a service token, used by CI or a script
	ActorOperator = "operator" // the installation's operator, with ADMIN_API_KEY
)

// AuditEntry is one thing a person — or a token, or the operator — did to an
// organization. Agent traffic has its own records; this is the other journal:
// who created a key, weakened a policy, invited someone, changed a role.
type AuditEntry struct {
	ID    int64     `json:"id"`
	OrgID string    `json:"-"`
	Time  time.Time `json:"time"`
	// ActorKind is one of the Actor* constants. ActorID is the user's or the
	// token's id; ActorLabel is what to call them — an email, a token's name —
	// kept on the entry so it stays readable after they are gone.
	ActorKind  string `json:"actor_kind"`
	ActorID    string `json:"actor_id,omitempty"`
	ActorLabel string `json:"actor_label"`
	// Action is a dotted code, "key.create", "policy.update": its first part
	// is the group the console filters by.
	Action string `json:"action"`
	// Target is what was acted on: a key's name, a member's email.
	Target string `json:"target,omitempty"`
	// Meta holds the details, before and after where there is one. Never a
	// secret: a key's value, a token, a webhook address.
	Meta map[string]any `json:"meta,omitempty"`
	IP   string         `json:"ip,omitempty"`
}

// AuditFilter narrows AuditStore.List. Zero values mean "any".
type AuditFilter struct {
	// Group is an action's first part: "key" matches "key.create" and
	// "key.delete" but not "keys.x".
	Group string
	// Before pages backwards: only entries with a smaller id.
	Before int64
	Limit  int
}

// AuditStore keeps the journal. It is append-only: there is deliberately no
// way to change or remove an entry through it.
type AuditStore interface {
	Record(ctx context.Context, e AuditEntry) error
	List(ctx context.Context, orgID string, f AuditFilter) ([]AuditEntry, error)
}

// MemAuditStore keeps the journal in memory, for tests.
type MemAuditStore struct {
	mu      sync.RWMutex
	entries []AuditEntry
	next    int64
}

var _ AuditStore = (*MemAuditStore)(nil)

func NewMemAuditStore() *MemAuditStore { return &MemAuditStore{} }

func (s *MemAuditStore) Record(_ context.Context, e AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Through JSON and back, as a database would store it: an entry is a
	// record of what was, and must not change when the caller's map does.
	if e.Meta != nil {
		raw, err := json.Marshal(e.Meta)
		if err != nil {
			return err
		}
		e.Meta = nil
		if err := json.Unmarshal(raw, &e.Meta); err != nil {
			return err
		}
	}
	s.next++
	e.ID = s.next
	e.Time = time.Now()
	s.entries = append(s.entries, e)
	return nil
}

func (s *MemAuditStore) List(_ context.Context, orgID string, f AuditFilter) ([]AuditEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	limit := auditLimit(f.Limit)
	out := []AuditEntry{}
	for i := len(s.entries) - 1; i >= 0 && len(out) < limit; i-- {
		e := s.entries[i]
		if e.OrgID != orgID {
			continue
		}
		if f.Before > 0 && e.ID >= f.Before {
			continue
		}
		if f.Group != "" && !strings.HasPrefix(e.Action, f.Group+".") {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// auditLimit bounds a page: a journal is read a screen at a time.
func auditLimit(n int) int {
	if n <= 0 || n > 500 {
		return 100
	}
	return n
}

// AuditLimit is auditLimit for other packages' stores.
func AuditLimit(n int) int { return auditLimit(n) }
