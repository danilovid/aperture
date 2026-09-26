package storagetest

import (
	"context"
	"testing"

	"github.com/mutegate/mutegate/internal/storage"
)

// RunAuditStore holds every AuditStore to the same contract. newStore returns
// the store and the id of a person who exists wherever that matters, to
// record as an actor.
func RunAuditStore(t *testing.T, newStore func(t *testing.T) (storage.AuditStore, string)) {
	t.Helper()
	ctx := context.Background()

	record := func(t *testing.T, s storage.AuditStore, orgID, action, target string) {
		t.Helper()
		if err := s.Record(ctx, storage.AuditEntry{
			OrgID: orgID, ActorKind: storage.ActorOperator, ActorLabel: "operator",
			Action: action, Target: target,
		}); err != nil {
			t.Fatalf("record %s: %v", action, err)
		}
	}

	t.Run("an entry round-trips", func(t *testing.T) {
		s, userID := newStore(t)
		in := storage.AuditEntry{
			OrgID: OrgA, ActorKind: storage.ActorUser, ActorID: userID, ActorLabel: "ann@acme.test",
			Action: "policy.update", Target: "ci-bot", IP: "203.0.113.7",
			Meta: map[string]any{"changes": []any{"secrets: block → alert"}, "weakened": true},
		}
		if err := s.Record(ctx, in); err != nil {
			t.Fatal(err)
		}
		got, err := s.List(ctx, OrgA, storage.AuditFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("want one entry, got %d", len(got))
		}
		e := got[0]
		if e.ID <= 0 || e.Time.IsZero() {
			t.Errorf("entry has no id or time: %+v", e)
		}
		if e.ActorKind != storage.ActorUser || e.ActorID != userID || e.ActorLabel != "ann@acme.test" ||
			e.Action != "policy.update" || e.Target != "ci-bot" || e.IP != "203.0.113.7" {
			t.Errorf("entry came back different: %+v", e)
		}
		changes, _ := e.Meta["changes"].([]any)
		if len(changes) != 1 || changes[0] != "secrets: block → alert" || e.Meta["weakened"] != true {
			t.Errorf("meta came back different: %#v", e.Meta)
		}
	})

	t.Run("a token and the operator are actors too", func(t *testing.T) {
		s, _ := newStore(t)
		s.Record(ctx, storage.AuditEntry{OrgID: OrgA, ActorKind: storage.ActorToken,
			ActorID: "tok-1", ActorLabel: "deploy-bot", Action: "key.create", Target: "ci"})
		record(t, s, OrgA, "organization.restore", "")
		got, _ := s.List(ctx, OrgA, storage.AuditFilter{})
		if len(got) != 2 || got[0].ActorKind != storage.ActorOperator ||
			got[1].ActorKind != storage.ActorToken || got[1].ActorID != "tok-1" || got[1].ActorLabel != "deploy-bot" {
			t.Errorf("actors came back different: %+v", got)
		}
		if got[0].Meta != nil {
			t.Errorf("an entry with no details grew some: %#v", got[0].Meta)
		}
	})

	t.Run("newest first, a page at a time", func(t *testing.T) {
		s, _ := newStore(t)
		for _, target := range []string{"one", "two", "three", "four", "five"} {
			record(t, s, OrgA, "key.create", target)
		}
		page, _ := s.List(ctx, OrgA, storage.AuditFilter{Limit: 2})
		if len(page) != 2 || page[0].Target != "five" || page[1].Target != "four" {
			t.Fatalf("first page: %+v", page)
		}
		next, _ := s.List(ctx, OrgA, storage.AuditFilter{Limit: 2, Before: page[1].ID})
		if len(next) != 2 || next[0].Target != "three" || next[1].Target != "two" {
			t.Fatalf("second page: %+v", next)
		}
		last, _ := s.List(ctx, OrgA, storage.AuditFilter{Limit: 2, Before: next[1].ID})
		if len(last) != 1 || last[0].Target != "one" {
			t.Fatalf("last page: %+v", last)
		}
	})

	t.Run("a group is an action's first part, exactly", func(t *testing.T) {
		s, _ := newStore(t)
		record(t, s, OrgA, "key.create", "a")
		record(t, s, OrgA, "key.delete", "a")
		record(t, s, OrgA, "keyring.open", "b")
		record(t, s, OrgA, "policy.update", "c")

		keys, _ := s.List(ctx, OrgA, storage.AuditFilter{Group: "key"})
		if len(keys) != 2 || keys[0].Action != "key.delete" || keys[1].Action != "key.create" {
			t.Errorf("group key: %+v", keys)
		}
		// A group is a name, not a pattern.
		for _, g := range []string{"%", "p_licy", "k%", "key."} {
			if got, _ := s.List(ctx, OrgA, storage.AuditFilter{Group: g}); len(got) != 0 {
				t.Errorf("group %q matched %d entries", g, len(got))
			}
		}
	})

	t.Run("one organization's journal is not another's", func(t *testing.T) {
		s, _ := newStore(t)
		record(t, s, OrgA, "key.create", "a-key")
		record(t, s, OrgB, "key.create", "b-key")
		a, _ := s.List(ctx, OrgA, storage.AuditFilter{})
		b, _ := s.List(ctx, OrgB, storage.AuditFilter{})
		if len(a) != 1 || a[0].Target != "a-key" || len(b) != 1 || b[0].Target != "b-key" {
			t.Errorf("journals leaked: A=%+v B=%+v", a, b)
		}
		// Paging by an id from another organization's journal shows nothing
		// of it either.
		if got, _ := s.List(ctx, OrgA, storage.AuditFilter{Before: b[0].ID + 1}); len(got) != 1 || got[0].Target != "a-key" {
			t.Errorf("paging crossed organizations: %+v", got)
		}
	})

	t.Run("an empty journal is an empty list", func(t *testing.T) {
		s, _ := newStore(t)
		got, err := s.List(ctx, OrgA, storage.AuditFilter{})
		if err != nil || got == nil || len(got) != 0 {
			t.Errorf("got %#v, %v; want an empty, non-nil list", got, err)
		}
	})
}
