// Package storagetest holds the behaviour every AccountStore must have,
// whichever database is underneath. Two implementations exist — PostgreSQL in
// production, memory in tests and no-DB mode — and a rule enforced in only
// one of them is a rule the tests cannot see.
package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/danilovid/aperture/internal/auth"
	"github.com/danilovid/aperture/internal/storage"
)

// RunAccountStore runs the whole contract against a store the caller builds.
// newStore is called per subtest so implementations may isolate state.
func RunAccountStore(t *testing.T, newStore func(t *testing.T) storage.AccountStore) {
	t.Helper()
	for name, test := range map[string]func(*testing.T, storage.AccountStore){
		"user lifecycle":             testUserLifecycle,
		"user without password":      testUserWithoutPassword,
		"organizations and roles":    testOrganizationsAndRoles,
		"invitations are single use": testInvitations,
		"session lifecycle":          testSessionLifecycle,
		"expired sessions":           testExpiredSessions,
		"log out everywhere":         testRevokeUserSessions,
	} {
		t.Run(name, func(t *testing.T) { test(t, newStore(t)) })
	}
}

func uniqueEmail(t *testing.T) string {
	t.Helper()
	return auth.NormalizeEmail(t.Name() + "-" + time.Now().Format("150405.000000000") + "@example.test")
}

func uniqueSlug(prefix string) string {
	return prefix + "-" + time.Now().Format("150405.000000000")
}

func testUserLifecycle(t *testing.T, s storage.AccountStore) {
	ctx := context.Background()
	email := uniqueEmail(t)

	hash, err := auth.HashPassword("a password worth keeping")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(ctx, email, "Ivan", hash)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.Email != email || u.PasswordHash != hash {
		t.Fatalf("created user looks wrong: %+v", u)
	}
	if u.LastLoginAt != nil {
		t.Error("a fresh user has already logged in")
	}

	if _, err := s.CreateUser(ctx, email, "Someone else", hash); !errors.Is(err, storage.ErrEmailTaken) {
		t.Errorf("duplicate address error = %v, want ErrEmailTaken", err)
	}

	got, err := s.UserByEmail(ctx, email)
	if err != nil || got.ID != u.ID {
		t.Fatalf("lookup by email: %+v %v", got, err)
	}
	if ok, err := auth.VerifyPassword(got.PasswordHash, "a password worth keeping"); err != nil || !ok {
		t.Error("the stored hash does not verify the password")
	}

	if _, err := s.UserByEmail(ctx, "nobody-"+email); !errors.Is(err, storage.ErrUserNotFound) {
		t.Errorf("missing user error = %v, want ErrUserNotFound", err)
	}

	if err := s.MarkLogin(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.UserByID(ctx, u.ID); got.LastLoginAt == nil {
		t.Error("last_login_at was not recorded")
	}
}

// An OAuth-only account has no password, and that must not read as "empty
// password accepted".
func testUserWithoutPassword(t *testing.T, s storage.AccountStore) {
	ctx := context.Background()

	u, err := s.CreateUser(ctx, uniqueEmail(t), "OAuth only", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.UserByID(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PasswordHash != "" {
		t.Errorf("password hash = %q, want empty", got.PasswordHash)
	}
	if ok, err := auth.VerifyPassword(got.PasswordHash, ""); ok || err == nil {
		t.Error("an empty hash verified something")
	}
}

func testOrganizationsAndRoles(t *testing.T, s storage.AccountStore) {
	ctx := context.Background()
	slug := uniqueSlug("acme")

	org, err := s.CreateOrganization(ctx, "Acme", slug)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOrganization(ctx, "Acme again", slug); !errors.Is(err, storage.ErrSlugTaken) {
		t.Errorf("duplicate slug error = %v, want ErrSlugTaken", err)
	}

	owner, err := s.CreateUser(ctx, "owner-"+uniqueEmail(t), "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	guest, err := s.CreateUser(ctx, "guest-"+uniqueEmail(t), "Guest", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AddMember(ctx, org.ID, owner.ID, storage.RoleOwner); err != nil {
		t.Fatal(err)
	}
	role, err := s.MemberRole(ctx, org.ID, owner.ID)
	if err != nil || role != storage.RoleOwner {
		t.Fatalf("role = %q err = %v", role, err)
	}
	if !role.AtLeast(storage.RoleAdmin) {
		t.Error("owner does not satisfy an admin requirement")
	}

	// Somebody who was never added is not a member, not a viewer.
	if _, err := s.MemberRole(ctx, org.ID, guest.ID); !errors.Is(err, storage.ErrNotMember) {
		t.Errorf("stranger role error = %v, want ErrNotMember", err)
	}

	if err := s.AddMember(ctx, org.ID, guest.ID, storage.RoleViewer); err != nil {
		t.Fatal(err)
	}
	members, err := s.MembersOf(ctx, org.ID)
	if err != nil || len(members) != 2 {
		t.Fatalf("members = %+v err = %v", members, err)
	}
	for _, m := range members {
		if m.PasswordHash != "" {
			t.Error("MembersOf leaks password hashes")
		}
	}

	if err := s.SetMemberRole(ctx, org.ID, guest.ID, storage.RoleMember); err != nil {
		t.Fatal(err)
	}
	if role, _ = s.MemberRole(ctx, org.ID, guest.ID); role != storage.RoleMember {
		t.Errorf("role after change = %q", role)
	}

	orgs, err := s.OrganizationsOf(ctx, guest.ID)
	if err != nil || len(orgs) != 1 || orgs[0].ID != org.ID || orgs[0].Role != storage.RoleMember {
		t.Fatalf("organizations of guest = %+v err = %v", orgs, err)
	}

	if err := s.RemoveMember(ctx, org.ID, guest.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMember(ctx, org.ID, guest.ID); !errors.Is(err, storage.ErrNotMember) {
		t.Errorf("removing twice = %v, want ErrNotMember", err)
	}
	if orgs, _ = s.OrganizationsOf(ctx, guest.ID); len(orgs) != 0 {
		t.Errorf("a removed member still sees the organization: %+v", orgs)
	}
}

func testSessionLifecycle(t *testing.T, s storage.AccountStore) {
	ctx := context.Background()

	u, err := s.CreateUser(ctx, uniqueEmail(t), "Ivan", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := s.CreateOrganization(ctx, "Session Co", uniqueSlug("sess"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, org.ID, u.ID, storage.RoleOwner); err != nil {
		t.Fatal(err)
	}

	token, hash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.CreateSession(ctx, storage.Session{
		UserID: u.ID, OrgID: org.ID, ExpiresAt: time.Now().Add(auth.SessionLifetime),
		IP: "203.0.113.5", UserAgent: "test",
	}, hash)
	if err != nil {
		t.Fatal(err)
	}

	gotSess, gotUser, err := s.SessionByToken(ctx, auth.HashSessionToken(token))
	if err != nil || gotSess.ID != sess.ID || gotUser.ID != u.ID {
		t.Fatalf("session lookup: %+v %+v %v", gotSess, gotUser, err)
	}
	if gotSess.OrgID != org.ID {
		t.Errorf("session org = %q, want %q", gotSess.OrgID, org.ID)
	}

	// A token nobody issued must not resolve.
	other, _, _ := auth.NewSessionToken()
	if _, _, err := s.SessionByToken(ctx, auth.HashSessionToken(other)); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Errorf("unknown token error = %v, want ErrSessionNotFound", err)
	}

	// Switching into an organization the user does not belong to is refused
	// by the store, not by the handler that happens to call it.
	stranger, err := s.CreateOrganization(ctx, "Not yours", uniqueSlug("other"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSessionOrg(ctx, sess.ID, stranger.ID); !errors.Is(err, storage.ErrNotMember) {
		t.Errorf("switching into a stranger's org = %v, want ErrNotMember", err)
	}

	if err := s.RevokeSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionByToken(ctx, auth.HashSessionToken(token)); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Errorf("a revoked session still resolves: %v", err)
	}
}

func testExpiredSessions(t *testing.T, s storage.AccountStore) {
	ctx := context.Background()

	u, err := s.CreateUser(ctx, uniqueEmail(t), "Ivan", "")
	if err != nil {
		t.Fatal(err)
	}
	token, hash, _ := auth.NewSessionToken()
	if _, err := s.CreateSession(ctx, storage.Session{
		UserID: u.ID, ExpiresAt: time.Now().Add(-time.Minute),
	}, hash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionByToken(ctx, auth.HashSessionToken(token)); !errors.Is(err, storage.ErrSessionNotFound) {
		t.Errorf("an expired session resolved: %v", err)
	}
}

func testRevokeUserSessions(t *testing.T, s storage.AccountStore) {
	ctx := context.Background()

	u, err := s.CreateUser(ctx, uniqueEmail(t), "Ivan", "")
	if err != nil {
		t.Fatal(err)
	}
	var tokens []string
	for i := 0; i < 3; i++ {
		token, hash, _ := auth.NewSessionToken()
		if _, err := s.CreateSession(ctx, storage.Session{
			UserID: u.ID, ExpiresAt: time.Now().Add(auth.SessionLifetime),
		}, hash); err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	if err := s.RevokeUserSessions(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if _, _, err := s.SessionByToken(ctx, auth.HashSessionToken(token)); !errors.Is(err, storage.ErrSessionNotFound) {
			t.Errorf("a session survived the log-out-everywhere: %v", err)
		}
	}
}

// An invitation is the only way in while registration is closed, so it must
// work exactly once and stop working when it expires.
func testInvitations(t *testing.T, s storage.AccountStore) {
	ctx := context.Background()

	org, err := s.CreateOrganization(ctx, "Invite Co", uniqueSlug("invite"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateUser(ctx, uniqueEmail(t), "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ctx, org.ID, admin.ID, storage.RoleAdmin); err != nil {
		t.Fatal(err)
	}

	token, hash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	inv, err := s.CreateInvitation(ctx, storage.Invitation{
		OrgID: org.ID, Email: "newcomer@example.test", Role: storage.RoleMember,
		InvitedBy: admin.ID, ExpiresAt: time.Now().Add(72 * time.Hour),
	}, hash)
	if err != nil {
		t.Fatal(err)
	}
	if inv.ID == "" || inv.Role != storage.RoleMember || inv.AcceptedAt != nil {
		t.Fatalf("created invitation looks wrong: %+v", inv)
	}

	got, err := s.InvitationByToken(ctx, auth.HashSessionToken(token))
	if err != nil || got.ID != inv.ID {
		t.Fatalf("lookup by token: %+v %v", got, err)
	}

	// A token nobody issued tells you nothing.
	other, _, _ := auth.NewSessionToken()
	if _, err := s.InvitationByToken(ctx, auth.HashSessionToken(other)); !errors.Is(err, storage.ErrInviteInvalid) {
		t.Errorf("unknown token error = %v, want ErrInviteInvalid", err)
	}

	if err := s.AcceptInvitation(ctx, inv.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptInvitation(ctx, inv.ID); !errors.Is(err, storage.ErrInviteInvalid) {
		t.Errorf("an invitation was accepted twice: %v", err)
	}
	if _, err := s.InvitationByToken(ctx, auth.HashSessionToken(token)); !errors.Is(err, storage.ErrInviteInvalid) {
		t.Errorf("an accepted invitation still resolves: %v", err)
	}

	// Expired invitations are dead on arrival.
	expiredToken, expiredHash, _ := auth.NewSessionToken()
	if _, err := s.CreateInvitation(ctx, storage.Invitation{
		OrgID: org.ID, Email: "late@example.test", Role: storage.RoleViewer,
		ExpiresAt: time.Now().Add(-time.Minute),
	}, expiredHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InvitationByToken(ctx, auth.HashSessionToken(expiredToken)); !errors.Is(err, storage.ErrInviteInvalid) {
		t.Errorf("an expired invitation resolved: %v", err)
	}

	list, err := s.InvitationsOf(ctx, org.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("invitations of org = %+v err = %v", list, err)
	}

	// Revoking belongs to the organization that owns the invitation.
	pendingID := ""
	for _, i := range list {
		if i.AcceptedAt == nil {
			pendingID = i.ID
		}
	}
	stranger, err := s.CreateOrganization(ctx, "Stranger", uniqueSlug("stranger"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeInvitation(ctx, stranger.ID, pendingID); !errors.Is(err, storage.ErrInviteInvalid) {
		t.Errorf("another organization revoked an invitation: %v", err)
	}
	if err := s.RevokeInvitation(ctx, org.ID, pendingID); err != nil {
		t.Errorf("the owning organization could not revoke: %v", err)
	}
}
