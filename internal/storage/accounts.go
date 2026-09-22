package storage

import (
	"context"
	"errors"
	"time"
)

// Role is what a person may do inside one organization. Roles never grant
// access across organizations: membership is checked first, the role second.
type Role string

const (
	RoleOwner  Role = "owner"  // billing, deleting the organization, handing it over
	RoleAdmin  Role = "admin"  // keys, policies, limits, providers, invitations
	RoleMember Role = "member" // read, playground, own keys
	RoleViewer Role = "viewer" // read only
)

// rank orders roles so a handler can ask for "admin or above" without
// enumerating the roles above admin every time.
var rank = map[Role]int{RoleViewer: 1, RoleMember: 2, RoleAdmin: 3, RoleOwner: 4}

// ValidRole reports whether s is a role this build knows.
func ValidRole(s string) bool { _, ok := rank[Role(s)]; return ok }

// AtLeast reports whether r carries at least the rights of min.
func (r Role) AtLeast(min Role) bool { return rank[r] >= rank[min] && rank[r] != 0 }

// Organization is a tenant. Every row of every other table belongs to one.
type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

// User is a person. PasswordHash is empty for accounts that only ever signed
// in through an identity provider.
type User struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	Name            string     `json:"name"`
	PasswordHash    string     `json:"-"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LastLoginAt     *time.Time `json:"last_login_at,omitempty"`
	Disabled        bool       `json:"disabled,omitempty"`
}

// Membership ties a person to an organization with a role.
type Membership struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// OrgMembership is an organization together with the caller's role in it —
// what the organization switcher needs in one query.
type OrgMembership struct {
	Organization
	Role Role `json:"role"`
}

// Member is a person together with their role — what the members screen shows.
type Member struct {
	User
	Role Role `json:"role"`
}

// Invitation is how a person gets into an organization: registration is
// closed, so an admin creates one of these and passes the link along. The
// token is stored hashed, like every other credential here.
type Invitation struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	Email      string     `json:"email"`
	Role       Role       `json:"role"`
	InvitedBy  string     `json:"invited_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
}

// Session is one signed-in browser. The token itself is never stored; the
// database holds its sha256, so a dump of this table cannot be replayed.
type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	OrgID      string    `json:"org_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

// Account errors. Callers turn these into answers that do not reveal whether
// an address is registered.
var (
	ErrUserNotFound    = errors.New("user not found")
	ErrOrgNotFound     = errors.New("organization not found")
	ErrSessionNotFound = errors.New("session not found")
	ErrEmailTaken      = errors.New("email already registered")
	ErrSlugTaken       = errors.New("organization slug already taken")
	ErrNotMember       = errors.New("not a member of this organization")
	// ErrInviteInvalid covers every reason an invitation cannot be used —
	// unknown, expired, already accepted. The caller must not tell them
	// apart either: a probe of invite tokens should learn nothing.
	ErrInviteInvalid = errors.New("invitation is not valid")
)

// AccountStore holds people, organizations and their sessions.
//
// It is deliberately separate from the stores that hold traffic data: those
// take an organization id on every method, because a query without one is a
// leak. This one is the layer that decides which organization a request is
// allowed to name in the first place.
type AccountStore interface {
	// People
	CreateUser(ctx context.Context, email, name, passwordHash string) (*User, error)
	UserByEmail(ctx context.Context, email string) (*User, error)
	UserByID(ctx context.Context, id string) (*User, error)
	SetPasswordHash(ctx context.Context, userID, hash string) error
	MarkLogin(ctx context.Context, userID string) error

	// Organizations and membership
	CreateOrganization(ctx context.Context, name, slug string) (*Organization, error)
	OrganizationByID(ctx context.Context, id string) (*Organization, error)
	AddMember(ctx context.Context, orgID, userID string, role Role) error
	MemberRole(ctx context.Context, orgID, userID string) (Role, error)
	OrganizationsOf(ctx context.Context, userID string) ([]OrgMembership, error)
	MembersOf(ctx context.Context, orgID string) ([]Member, error)
	SetMemberRole(ctx context.Context, orgID, userID string, role Role) error
	RemoveMember(ctx context.Context, orgID, userID string) error

	// Invitations
	CreateInvitation(ctx context.Context, inv Invitation, tokenHash []byte) (*Invitation, error)
	InvitationByToken(ctx context.Context, tokenHash []byte) (*Invitation, error)
	AcceptInvitation(ctx context.Context, id string) error
	InvitationsOf(ctx context.Context, orgID string) ([]Invitation, error)
	RevokeInvitation(ctx context.Context, orgID, id string) error

	// Sessions
	CreateSession(ctx context.Context, s Session, tokenHash []byte) (*Session, error)
	SessionByToken(ctx context.Context, tokenHash []byte) (*Session, *User, error)
	RenewSession(ctx context.Context, id string, expiresAt time.Time) error
	SetSessionOrg(ctx context.Context, id, orgID string) error
	RevokeSession(ctx context.Context, id string) error
	RevokeUserSessions(ctx context.Context, userID string) error
}
