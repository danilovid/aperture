package storage

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemAccountStore keeps accounts in memory. It exists for the same reason the
// other Mem* stores do: tests and a gateway started without a database should
// behave the same way as one with it, so the rules live here too rather than
// in the handlers.
type MemAccountStore struct {
	mu       sync.RWMutex
	users    map[string]*User  // id → user
	byEmail  map[string]string // email → id
	orgs     map[string]*Organization
	bySlug   map[string]string        // slug → id
	members  map[string]Role          // orgID+"|"+userID → role
	joined   map[string]time.Time     // orgID+"|"+userID → when
	invites  map[string]*Invitation   // id → invitation
	inviteBy map[string]string        // hex(tokenHash) → invitation id
	sessions map[string]*Session      // id → session
	sessBy   map[string]string        // hex(tokenHash) → session id
	revoked  map[string]bool          // session id → revoked
	tokens   map[string]*ServiceToken // id → service token
	tokenBy  map[string]string        // hex(tokenHash) → service token id
	idents   map[string]*Identity     // id → identity
	identBy  map[string]string        // provider+"|"+providerUserID → identity id
}

var _ AccountStore = (*MemAccountStore)(nil)

// NewMemAccountStore returns an empty in-memory account store.
func NewMemAccountStore() *MemAccountStore {
	return &MemAccountStore{
		users:    map[string]*User{},
		byEmail:  map[string]string{},
		orgs:     map[string]*Organization{},
		bySlug:   map[string]string{},
		members:  map[string]Role{},
		joined:   map[string]time.Time{},
		invites:  map[string]*Invitation{},
		inviteBy: map[string]string{},
		sessions: map[string]*Session{},
		sessBy:   map[string]string{},
		revoked:  map[string]bool{},
		tokens:   map[string]*ServiceToken{},
		tokenBy:  map[string]string{},
		idents:   map[string]*Identity{},
		identBy:  map[string]string{},
	}
}

func memberKey(orgID, userID string) string { return orgID + "|" + userID }

// tokenKey turns a hash into a map key. The hash is bytes; the map needs a
// comparable value, and a string of those bytes is exactly that.
func tokenKey(hash []byte) string { return string(hash) }

// ── people ───────────────────────────────────────────────────────────────────

func (s *MemAccountStore) CreateUser(_ context.Context, email, name, passwordHash string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.byEmail[email]; taken {
		return nil, ErrEmailTaken
	}
	u := &User{
		ID: uuid.NewString(), Email: email, Name: name,
		PasswordHash: passwordHash, CreatedAt: time.Now(),
	}
	s.users[u.ID] = u
	s.byEmail[email] = u.ID
	copy := *u
	return &copy, nil
}

func (s *MemAccountStore) UserByEmail(_ context.Context, email string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byEmail[email]
	if !ok {
		return nil, ErrUserNotFound
	}
	copy := *s.users[id]
	return &copy, nil
}

func (s *MemAccountStore) UserByID(_ context.Context, id string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrUserNotFound
	}
	copy := *u
	return &copy, nil
}

func (s *MemAccountStore) SetPasswordHash(_ context.Context, userID, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	u.PasswordHash = hash
	return nil
}

func (s *MemAccountStore) MarkLogin(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	now := time.Now()
	u.LastLoginAt = &now
	return nil
}

// ── organizations ────────────────────────────────────────────────────────────

func (s *MemAccountStore) CreateOrganization(_ context.Context, name, slug string) (*Organization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.bySlug[slug]; taken {
		return nil, ErrSlugTaken
	}
	o := &Organization{ID: uuid.NewString(), Name: name, Slug: slug, CreatedAt: time.Now()}
	s.orgs[o.ID] = o
	s.bySlug[slug] = o.ID
	copy := *o
	return &copy, nil
}

func (s *MemAccountStore) OrganizationByID(_ context.Context, id string) (*Organization, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.orgs[id]
	if !ok {
		return nil, ErrOrgNotFound
	}
	copy := *o
	return &copy, nil
}

func (s *MemAccountStore) RenameOrganization(_ context.Context, orgID, name string) (*Organization, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orgs[orgID]
	if !ok {
		return nil, ErrOrgNotFound
	}
	o.Name = name
	copy := *o
	return &copy, nil
}

func (s *MemAccountStore) DeleteOrganization(_ context.Context, orgID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orgs[orgID]
	if !ok {
		return ErrOrgNotFound
	}
	if o.DeletedAt == nil {
		now := time.Now()
		o.DeletedAt = &now
	}
	return nil
}

func (s *MemAccountStore) RestoreOrganization(_ context.Context, orgID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orgs[orgID]
	if !ok {
		return ErrOrgNotFound
	}
	o.DeletedAt = nil
	return nil
}

// ── identities ───────────────────────────────────────────────────────────────

func identKey(provider, providerUserID string) string { return provider + "|" + providerUserID }

func (s *MemAccountStore) UserByIdentity(_ context.Context, provider, providerUserID string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.identBy[identKey(provider, providerUserID)]
	if !ok {
		return nil, ErrUserNotFound
	}
	u, ok := s.users[s.idents[id].UserID]
	if !ok {
		return nil, ErrUserNotFound
	}
	copy := *u
	return &copy, nil
}

func (s *MemAccountStore) LinkIdentity(_ context.Context, userID, provider, providerUserID, email string) (*Identity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[userID]; !ok {
		return nil, ErrUserNotFound
	}
	if _, taken := s.identBy[identKey(provider, providerUserID)]; taken {
		return nil, ErrIdentityTaken
	}
	ident := &Identity{
		ID: uuid.NewString(), UserID: userID, Provider: provider,
		ProviderUserID: providerUserID, Email: email, CreatedAt: time.Now(),
	}
	s.idents[ident.ID] = ident
	s.identBy[identKey(provider, providerUserID)] = ident.ID
	copy := *ident
	return &copy, nil
}

func (s *MemAccountStore) IdentitiesOf(_ context.Context, userID string) ([]Identity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Identity{}
	for _, ident := range s.idents {
		if ident.UserID == userID {
			out = append(out, *ident)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemAccountStore) UnlinkIdentity(_ context.Context, userID, identityID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ident, ok := s.idents[identityID]
	// Somebody else's identity is not found, rather than forbidden: its id
	// should tell a stranger nothing.
	if !ok || ident.UserID != userID {
		return ErrIdentityNotFound
	}
	delete(s.identBy, identKey(ident.Provider, ident.ProviderUserID))
	delete(s.idents, identityID)
	return nil
}

// ── service tokens ───────────────────────────────────────────────────────────

func (s *MemAccountStore) CreateServiceToken(_ context.Context, in ServiceToken, tokenHash []byte) (*ServiceToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := in
	t.ID = uuid.NewString()
	t.CreatedAt = time.Now()
	s.tokens[t.ID] = &t
	s.tokenBy[tokenKey(tokenHash)] = t.ID
	copy := t
	return &copy, nil
}

func (s *MemAccountStore) ServiceTokenByHash(_ context.Context, tokenHash []byte) (*ServiceToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.tokenBy[tokenKey(tokenHash)]
	if !ok {
		return nil, ErrTokenInvalid
	}
	t := s.tokens[id]
	// Unknown, revoked and expired are one answer: probing tokens teaches
	// nothing about which of them a value was.
	if t == nil || t.RevokedAt != nil {
		return nil, ErrTokenInvalid
	}
	if t.ExpiresAt != nil && !t.ExpiresAt.After(time.Now()) {
		return nil, ErrTokenInvalid
	}
	if org := s.orgs[t.OrgID]; org == nil || org.DeletedAt != nil {
		return nil, ErrTokenInvalid
	}
	copy := *t
	copy.Scopes = append([]Scope(nil), t.Scopes...)
	return &copy, nil
}

func (s *MemAccountStore) ServiceTokensOf(_ context.Context, orgID string) ([]ServiceToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []ServiceToken
	for _, t := range s.tokens {
		if t.OrgID != orgID || t.RevokedAt != nil {
			continue
		}
		copy := *t
		copy.Scopes = append([]Scope(nil), t.Scopes...)
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemAccountStore) RevokeServiceToken(_ context.Context, orgID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tokens[id]
	// The organization is part of the lookup, not a check afterwards: one
	// organization must not be able to revoke another's token by guessing an
	// id, and must not learn whether that id exists either.
	if t == nil || t.OrgID != orgID || t.RevokedAt != nil {
		return ErrTokenInvalid
	}
	now := time.Now()
	t.RevokedAt = &now
	return nil
}

func (s *MemAccountStore) TouchServiceToken(_ context.Context, orgID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tokens[id]
	if t == nil || t.OrgID != orgID {
		return ErrTokenInvalid
	}
	now := time.Now()
	t.LastUsedAt = &now
	return nil
}

func (s *MemAccountStore) AddMember(_ context.Context, orgID, userID string, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey(orgID, userID)
	if _, exists := s.members[k]; !exists {
		s.joined[k] = time.Now()
	}
	s.members[k] = role
	return nil
}

func (s *MemAccountStore) MemberRole(_ context.Context, orgID, userID string) (Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, ok := s.members[memberKey(orgID, userID)]
	if !ok {
		return "", ErrNotMember
	}
	return role, nil
}

func (s *MemAccountStore) OrganizationsOf(_ context.Context, userID string) ([]OrgMembership, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []OrgMembership{}
	for k, role := range s.members {
		orgID, member := splitKey(k)
		if member != userID {
			continue
		}
		org, ok := s.orgs[orgID]
		if !ok || org.DeletedAt != nil {
			continue
		}
		out = append(out, OrgMembership{Organization: *org, Role: role})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemAccountStore) MembersOf(_ context.Context, orgID string) ([]Member, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Member{}
	for k, role := range s.members {
		org, userID := splitKey(k)
		if org != orgID {
			continue
		}
		u, ok := s.users[userID]
		if !ok {
			continue
		}
		copy := *u
		copy.PasswordHash = "" // the members screen has no business with hashes
		out = append(out, Member{User: copy, Role: role})
	}
	sort.Slice(out, func(i, j int) bool {
		return s.joined[memberKey(orgID, out[i].ID)].Before(s.joined[memberKey(orgID, out[j].ID)])
	})
	return out, nil
}

func (s *MemAccountStore) SetMemberRole(_ context.Context, orgID, userID string, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey(orgID, userID)
	if _, ok := s.members[k]; !ok {
		return ErrNotMember
	}
	s.members[k] = role
	return nil
}

func (s *MemAccountStore) RemoveMember(_ context.Context, orgID, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey(orgID, userID)
	if _, ok := s.members[k]; !ok {
		return ErrNotMember
	}
	delete(s.members, k)
	delete(s.joined, k)
	return nil
}

func splitKey(k string) (orgID, userID string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// ── invitations ──────────────────────────────────────────────────────────────

func (s *MemAccountStore) CreateInvitation(_ context.Context, in Invitation, tokenHash []byte) (*Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv := in
	inv.ID = uuid.NewString()
	inv.CreatedAt = time.Now()
	s.invites[inv.ID] = &inv
	s.inviteBy[tokenKey(tokenHash)] = inv.ID
	copy := inv
	return &copy, nil
}

func (s *MemAccountStore) InvitationByToken(_ context.Context, tokenHash []byte) (*Invitation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.inviteBy[tokenKey(tokenHash)]
	if !ok {
		return nil, ErrInviteInvalid
	}
	inv := s.invites[id]
	if inv == nil || inv.AcceptedAt != nil || !inv.ExpiresAt.After(time.Now()) {
		return nil, ErrInviteInvalid
	}
	copy := *inv
	return &copy, nil
}

func (s *MemAccountStore) AcceptInvitation(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[id]
	if !ok || inv.AcceptedAt != nil || !inv.ExpiresAt.After(time.Now()) {
		return ErrInviteInvalid
	}
	now := time.Now()
	inv.AcceptedAt = &now
	return nil
}

func (s *MemAccountStore) InvitationsOf(_ context.Context, orgID string) ([]Invitation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Invitation{}
	for _, inv := range s.invites {
		if inv.OrgID == orgID {
			out = append(out, *inv)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *MemAccountStore) RevokeInvitation(_ context.Context, orgID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invites[id]
	if !ok || inv.OrgID != orgID || inv.AcceptedAt != nil {
		return ErrInviteInvalid
	}
	delete(s.invites, id)
	for token, invID := range s.inviteBy {
		if invID == id {
			delete(s.inviteBy, token)
		}
	}
	return nil
}

// ── sessions ─────────────────────────────────────────────────────────────────

func (s *MemAccountStore) CreateSession(_ context.Context, in Session, tokenHash []byte) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := in
	sess.ID = uuid.NewString()
	sess.CreatedAt = time.Now()
	sess.LastSeenAt = sess.CreatedAt
	s.sessions[sess.ID] = &sess
	s.sessBy[tokenKey(tokenHash)] = sess.ID
	copy := sess
	return &copy, nil
}

func (s *MemAccountStore) SessionByToken(_ context.Context, tokenHash []byte) (*Session, *User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.sessBy[tokenKey(tokenHash)]
	if !ok {
		return nil, nil, ErrSessionNotFound
	}
	sess, ok := s.sessions[id]
	if !ok || s.revoked[id] || !sess.ExpiresAt.After(time.Now()) {
		return nil, nil, ErrSessionNotFound
	}
	u, ok := s.users[sess.UserID]
	if !ok {
		return nil, nil, ErrUserNotFound
	}
	sess.LastSeenAt = time.Now()
	sessCopy, userCopy := *sess, *u
	return &sessCopy, &userCopy, nil
}

func (s *MemAccountStore) RenewSession(_ context.Context, id string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || s.revoked[id] {
		return ErrSessionNotFound
	}
	sess.ExpiresAt = expiresAt
	return nil
}

// SetSessionOrg refuses an organization the session's user does not belong
// to, exactly as the SQL version does: the check belongs to the store, not to
// whichever handler happens to call it.
func (s *MemAccountStore) SetSessionOrg(_ context.Context, id, orgID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || s.revoked[id] {
		return ErrSessionNotFound
	}
	if _, member := s.members[memberKey(orgID, sess.UserID)]; !member {
		return ErrNotMember
	}
	if org := s.orgs[orgID]; org == nil || org.DeletedAt != nil {
		return ErrNotMember
	}
	sess.OrgID = orgID
	return nil
}

func (s *MemAccountStore) RevokeSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revoked[id] = true
	return nil
}

func (s *MemAccountStore) RevokeUserSessions(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.sessions {
		if sess.UserID == userID {
			s.revoked[id] = true
		}
	}
	return nil
}
