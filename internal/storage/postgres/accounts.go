package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mutegate/mutegate/internal/storage"
)

// The accounts schema. Everything here is about people; the traffic tables
// gain their org_id separately, so that this half can land before the
// isolation pass and an existing install keeps working in between.
const accountsSchema = `
CREATE TABLE IF NOT EXISTS users (
	id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	email             TEXT UNIQUE NOT NULL,
	name              TEXT NOT NULL DEFAULT '',
	-- Null for accounts that only ever arrived through an identity provider.
	password_hash     TEXT,
	email_verified_at TIMESTAMPTZ,
	created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	last_login_at     TIMESTAMPTZ,
	disabled_at       TIMESTAMPTZ
);

-- One person may attach Google today and GitHub tomorrow, so identities live
-- beside the user rather than inside it.
CREATE TABLE IF NOT EXISTS user_identities (
	id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	provider         TEXT NOT NULL,
	provider_user_id TEXT NOT NULL,
	email            TEXT NOT NULL DEFAULT '',
	created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	UNIQUE(provider, provider_user_id)
);

CREATE TABLE IF NOT EXISTS memberships (
	org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	role       TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (org_id, user_id)
);

CREATE TABLE IF NOT EXISTS sessions (
	id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	current_org_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
	-- sha256 of the token the browser holds; the token itself is never stored.
	token_hash     BYTEA UNIQUE NOT NULL,
	created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	expires_at     TIMESTAMPTZ NOT NULL,
	last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	ip             TEXT NOT NULL DEFAULT '',
	user_agent     TEXT NOT NULL DEFAULT '',
	revoked_at     TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS invitations (
	id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	email      TEXT NOT NULL,
	role       TEXT NOT NULL,
	token_hash BYTEA UNIQUE NOT NULL,
	invited_by UUID REFERENCES users(id) ON DELETE SET NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	expires_at TIMESTAMPTZ NOT NULL,
	accepted_at TIMESTAMPTZ
);

-- What people did, as opposed to what agents sent. Kept per organization
-- because that is who gets asked about it.
CREATE TABLE IF NOT EXISTS audit_log (
	id            BIGSERIAL PRIMARY KEY,
	org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
	action        TEXT NOT NULL,
	target        TEXT NOT NULL DEFAULT '',
	meta          JSONB NOT NULL DEFAULT '{}'::jsonb,
	ip            TEXT NOT NULL DEFAULT '',
	created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Credentials for CI and scripts. Like every other credential here, only the
-- hash is stored, and the scopes say what it may do rather than who it is.
CREATE TABLE IF NOT EXISTS service_tokens (
	id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
	org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
	name         TEXT NOT NULL,
	scopes       TEXT[] NOT NULL DEFAULT '{}',
	token_hash   BYTEA UNIQUE NOT NULL,
	hint         TEXT NOT NULL DEFAULT '',
	created_by   UUID REFERENCES users(id) ON DELETE SET NULL,
	created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	expires_at   TIMESTAMPTZ,
	last_used_at TIMESTAMPTZ,
	revoked_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_service_tokens_org ON service_tokens(org_id);
CREATE INDEX IF NOT EXISTS idx_memberships_user ON memberships(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_invitations_org ON invitations(org_id);
CREATE INDEX IF NOT EXISTS idx_audit_log_org_ts ON audit_log(org_id, created_at DESC);
`

// AccountStore implements storage.AccountStore on PostgreSQL.
type AccountStore struct {
	pool *pgxpool.Pool
}

var _ storage.AccountStore = (*AccountStore)(nil)

// NewAccountStore ensures the schema exists.
func NewAccountStore(ctx context.Context, pool *pgxpool.Pool) (*AccountStore, error) {
	if err := ensureTenancy(ctx, pool); err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, accountsSchema); err != nil {
		return nil, fmt.Errorf("init accounts schema: %w", err)
	}
	return &AccountStore{pool: pool}, nil
}

// isUnique reports whether err is a unique-violation on the named constraint
// fragment, so a duplicate address becomes ErrEmailTaken rather than a 500.
func isUnique(err error, fragment string) bool {
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) || pgErr.SQLState() != "23505" {
		return false
	}
	return fragment == "" || strings.Contains(err.Error(), fragment)
}

// ── people ───────────────────────────────────────────────────────────────────

func (s *AccountStore) CreateUser(ctx context.Context, email, name, passwordHash string) (*storage.User, error) {
	var hash *string
	if passwordHash != "" {
		hash = &passwordHash
	}
	var u storage.User
	var pw *string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash)
		VALUES ($1, $2, $3)
		RETURNING id::text, email, name, password_hash, email_verified_at, created_at, last_login_at,
		          disabled_at IS NOT NULL`,
		email, name, hash,
	).Scan(&u.ID, &u.Email, &u.Name, &pw, &u.EmailVerifiedAt, &u.CreatedAt, &u.LastLoginAt, &u.Disabled)
	if err != nil {
		if isUnique(err, "users_email") {
			return nil, storage.ErrEmailTaken
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	if pw != nil {
		u.PasswordHash = *pw
	}
	return &u, nil
}

const userColumns = `id::text, email, name, password_hash, email_verified_at, created_at,
	last_login_at, disabled_at IS NOT NULL`

func (s *AccountStore) scanUser(row pgx.Row) (*storage.User, error) {
	var u storage.User
	var pw *string
	err := row.Scan(&u.ID, &u.Email, &u.Name, &pw, &u.EmailVerifiedAt, &u.CreatedAt, &u.LastLoginAt, &u.Disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	if pw != nil {
		u.PasswordHash = *pw
	}
	return &u, nil
}

func (s *AccountStore) UserByEmail(ctx context.Context, email string) (*storage.User, error) {
	return s.scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = $1`, email))
}

func (s *AccountStore) UserByID(ctx context.Context, id string) (*storage.User, error) {
	return s.scanUser(s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1::uuid`, id))
}

func (s *AccountStore) SetPasswordHash(ctx context.Context, userID, hash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1::uuid`, userID, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrUserNotFound
	}
	return nil
}

func (s *AccountStore) MarkLogin(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at = NOW() WHERE id = $1::uuid`, userID)
	return err
}

// ── identities ───────────────────────────────────────────────────────────────

func (s *AccountStore) UserByIdentity(ctx context.Context, provider, providerUserID string) (*storage.User, error) {
	return s.scanUser(s.pool.QueryRow(ctx, `
		SELECT `+userColumns+` FROM users
		WHERE id = (SELECT user_id FROM user_identities WHERE provider = $1 AND provider_user_id = $2)`,
		provider, providerUserID))
}

func (s *AccountStore) LinkIdentity(ctx context.Context, userID, provider, providerUserID, email string) (*storage.Identity, error) {
	var i storage.Identity
	err := s.pool.QueryRow(ctx, `
		INSERT INTO user_identities (user_id, provider, provider_user_id, email)
		VALUES ($1::uuid, $2, $3, $4)
		RETURNING id::text, user_id::text, provider, provider_user_id, email, created_at`,
		userID, provider, providerUserID, email,
	).Scan(&i.ID, &i.UserID, &i.Provider, &i.ProviderUserID, &i.Email, &i.CreatedAt)
	if err != nil {
		if isUnique(err, "user_identities") {
			return nil, storage.ErrIdentityTaken
		}
		return nil, fmt.Errorf("link identity: %w", err)
	}
	return &i, nil
}

func (s *AccountStore) IdentitiesOf(ctx context.Context, userID string) ([]storage.Identity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, user_id::text, provider, provider_user_id, email, created_at
		FROM user_identities WHERE user_id = $1::uuid ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.Identity{}
	for rows.Next() {
		var i storage.Identity
		if err := rows.Scan(&i.ID, &i.UserID, &i.Provider, &i.ProviderUserID, &i.Email, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *AccountStore) UnlinkIdentity(ctx context.Context, userID, identityID string) error {
	// The owner is part of the WHERE: somebody else's identity is simply not
	// found, and its id tells a stranger nothing.
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM user_identities WHERE id = $1::uuid AND user_id = $2::uuid`, identityID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrIdentityNotFound
	}
	return nil
}

// ── organizations ────────────────────────────────────────────────────────────

func (s *AccountStore) CreateOrganization(ctx context.Context, name, slug string) (*storage.Organization, error) {
	var o storage.Organization
	err := s.pool.QueryRow(ctx, `
		INSERT INTO organizations (name, slug) VALUES ($1, $2)
		RETURNING id::text, name, slug, created_at`, name, slug,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
	if err != nil {
		if isUnique(err, "organizations_slug") {
			return nil, storage.ErrSlugTaken
		}
		return nil, fmt.Errorf("create organization: %w", err)
	}
	return &o, nil
}

func (s *AccountStore) OrganizationByID(ctx context.Context, id string) (*storage.Organization, error) {
	var o storage.Organization
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, name, slug, created_at, deleted_at FROM organizations
		WHERE id = $1::uuid`, id,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt, &o.DeletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrOrgNotFound
	}
	return &o, err
}

func (s *AccountStore) RenameOrganization(ctx context.Context, orgID, name string) (*storage.Organization, error) {
	var o storage.Organization
	err := s.pool.QueryRow(ctx, `
		UPDATE organizations SET name = $2
		WHERE id = $1::uuid AND deleted_at IS NULL
		RETURNING id::text, name, slug, created_at`, orgID, name,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrOrgNotFound
	}
	return &o, err
}

func (s *AccountStore) DeleteOrganization(ctx context.Context, orgID string) error {
	// Already deleted is not an error: the caller asked for it to be gone and
	// it is gone. NOW() only on the first one, so the recovery window is
	// measured from the deletion that actually happened.
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET deleted_at = NOW() WHERE id = $1::uuid AND deleted_at IS NULL`, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either no such organization or it was already deleted; the second
		// is success, so only a missing row is a failure.
		var exists bool
		if err := s.pool.QueryRow(ctx,
			`SELECT true FROM organizations WHERE id = $1::uuid`, orgID).Scan(&exists); err != nil {
			return storage.ErrOrgNotFound
		}
	}
	return nil
}

func (s *AccountStore) RestoreOrganization(ctx context.Context, orgID string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET deleted_at = NULL WHERE id = $1::uuid`, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrOrgNotFound
	}
	return nil
}

func (s *AccountStore) AddMember(ctx context.Context, orgID, userID string, role storage.Role) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO memberships (org_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)
		ON CONFLICT (org_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		orgID, userID, string(role))
	return err
}

func (s *AccountStore) MemberRole(ctx context.Context, orgID, userID string) (storage.Role, error) {
	var role string
	err := s.pool.QueryRow(ctx,
		`SELECT role FROM memberships WHERE org_id = $1::uuid AND user_id = $2::uuid`,
		orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", storage.ErrNotMember
	}
	return storage.Role(role), err
}

func (s *AccountStore) OrganizationsOf(ctx context.Context, userID string) ([]storage.OrgMembership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id::text, o.name, o.slug, o.created_at, m.role
		FROM memberships m
		JOIN organizations o ON o.id = m.org_id
		WHERE m.user_id = $1::uuid AND o.deleted_at IS NULL
		ORDER BY o.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []storage.OrgMembership{}
	for rows.Next() {
		var m storage.OrgMembership
		var role string
		if err := rows.Scan(&m.ID, &m.Name, &m.Slug, &m.CreatedAt, &role); err != nil {
			return nil, err
		}
		m.Role = storage.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *AccountStore) MembersOf(ctx context.Context, orgID string) ([]storage.Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, u.email, u.name, u.email_verified_at, u.created_at, u.last_login_at,
		       u.disabled_at IS NOT NULL, m.role
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.org_id = $1::uuid
		ORDER BY m.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []storage.Member{}
	for rows.Next() {
		var m storage.Member
		var role string
		if err := rows.Scan(&m.ID, &m.Email, &m.Name, &m.EmailVerifiedAt, &m.CreatedAt,
			&m.LastLoginAt, &m.Disabled, &role); err != nil {
			return nil, err
		}
		m.Role = storage.Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *AccountStore) SetMemberRole(ctx context.Context, orgID, userID string, role storage.Role) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE memberships SET role = $3 WHERE org_id = $1::uuid AND user_id = $2::uuid`,
		orgID, userID, string(role))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotMember
	}
	return nil
}

func (s *AccountStore) RemoveMember(ctx context.Context, orgID, userID string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM memberships WHERE org_id = $1::uuid AND user_id = $2::uuid`, orgID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotMember
	}
	return nil
}

// ── service tokens ───────────────────────────────────────────────────────────

// tokenColumns is the shape every service-token read returns, so the scans
// below cannot drift apart from each other.
const tokenColumns = `id::text, org_id::text, name, scopes, hint,
	COALESCE(created_by::text, ''), created_at, expires_at, last_used_at, revoked_at`

func scanToken(row pgx.Row) (*storage.ServiceToken, error) {
	var t storage.ServiceToken
	var scopes []string
	err := row.Scan(&t.ID, &t.OrgID, &t.Name, &scopes, &t.Hint,
		&t.CreatedBy, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt)
	if err != nil {
		return nil, err
	}
	for _, sc := range scopes {
		t.Scopes = append(t.Scopes, storage.Scope(sc))
	}
	return &t, nil
}

func (s *AccountStore) CreateServiceToken(ctx context.Context, in storage.ServiceToken, tokenHash []byte) (*storage.ServiceToken, error) {
	scopes := make([]string, 0, len(in.Scopes))
	for _, sc := range in.Scopes {
		scopes = append(scopes, string(sc))
	}
	var createdBy *string
	if in.CreatedBy != "" {
		createdBy = &in.CreatedBy
	}
	t, err := scanToken(s.pool.QueryRow(ctx, `
		INSERT INTO service_tokens (org_id, name, scopes, token_hash, hint, created_by, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7)
		RETURNING `+tokenColumns,
		in.OrgID, in.Name, scopes, tokenHash, in.Hint, createdBy, in.ExpiresAt))
	if err != nil {
		return nil, fmt.Errorf("create service token: %w", err)
	}
	return t, nil
}

func (s *AccountStore) ServiceTokenByHash(ctx context.Context, tokenHash []byte) (*storage.ServiceToken, error) {
	// Revoked, expired and unknown are one answer, and the organization must
	// be alive: a deleted organization's tokens stop working with everything
	// else of its.
	t, err := scanToken(s.pool.QueryRow(ctx, `
		SELECT `+tokenColumns+` FROM service_tokens t
		WHERE t.token_hash = $1
		  AND t.revoked_at IS NULL
		  AND (t.expires_at IS NULL OR t.expires_at > NOW())
		  AND EXISTS (SELECT 1 FROM organizations o
		              WHERE o.id = t.org_id AND o.deleted_at IS NULL)`, tokenHash))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrTokenInvalid
	}
	return t, err
}

func (s *AccountStore) ServiceTokensOf(ctx context.Context, orgID string) ([]storage.ServiceToken, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+tokenColumns+` FROM service_tokens
		WHERE org_id = $1::uuid AND revoked_at IS NULL
		ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []storage.ServiceToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *AccountStore) RevokeServiceToken(ctx context.Context, orgID, id string) error {
	// The organization is part of the WHERE, not a check afterwards: one
	// organization must not be able to revoke another's token by guessing.
	tag, err := s.pool.Exec(ctx, `
		UPDATE service_tokens SET revoked_at = NOW()
		WHERE id = $1::uuid AND org_id = $2::uuid AND revoked_at IS NULL`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrTokenInvalid
	}
	return nil
}

func (s *AccountStore) TouchServiceToken(ctx context.Context, orgID, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE service_tokens SET last_used_at = NOW()
		 WHERE id = $1::uuid AND org_id = $2::uuid`, id, orgID)
	return err
}

// ── sessions ─────────────────────────────────────────────────────────────────

func (s *AccountStore) CreateSession(ctx context.Context, in storage.Session, tokenHash []byte) (*storage.Session, error) {
	var out storage.Session
	var orgID *string
	if in.OrgID != "" {
		orgID = &in.OrgID
	}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, current_org_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)
		RETURNING id::text, user_id::text, COALESCE(current_org_id::text, ''), created_at,
		          expires_at, last_seen_at, ip, user_agent`,
		in.UserID, orgID, tokenHash, in.ExpiresAt, in.IP, in.UserAgent,
	).Scan(&out.ID, &out.UserID, &out.OrgID, &out.CreatedAt, &out.ExpiresAt, &out.LastSeenAt,
		&out.IP, &out.UserAgent)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}
	return &out, nil
}

// SessionByToken resolves a presented token to its session and user in one
// round trip, skipping sessions that are revoked or expired. Touching
// last_seen_at here keeps "active sessions" honest without a second write.
func (s *AccountStore) SessionByToken(ctx context.Context, tokenHash []byte) (*storage.Session, *storage.User, error) {
	var sess storage.Session
	var u storage.User
	var pw *string
	err := s.pool.QueryRow(ctx, `
		UPDATE sessions SET last_seen_at = NOW()
		WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > NOW()
		RETURNING id::text, user_id::text, COALESCE(current_org_id::text, ''), created_at,
		          expires_at, last_seen_at, ip, user_agent`, tokenHash,
	).Scan(&sess.ID, &sess.UserID, &sess.OrgID, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt,
		&sess.IP, &sess.UserAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, storage.ErrSessionNotFound
	}
	if err != nil {
		return nil, nil, err
	}

	err = s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1::uuid`, sess.UserID).
		Scan(&u.ID, &u.Email, &u.Name, &pw, &u.EmailVerifiedAt, &u.CreatedAt, &u.LastLoginAt, &u.Disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, storage.ErrUserNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if pw != nil {
		u.PasswordHash = *pw
	}
	return &sess, &u, nil
}

func (s *AccountStore) RenewSession(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET expires_at = $2 WHERE id = $1::uuid AND revoked_at IS NULL`, id, expiresAt)
	return err
}

func (s *AccountStore) SetSessionOrg(ctx context.Context, id, orgID string) error {
	// The organization must be one the session's user actually belongs to and
	// one that still exists; checking both here means no handler can switch a
	// session into a stranger's data, or into a closed organization, by
	// passing an id.
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET current_org_id = $2::uuid
		WHERE id = $1::uuid AND revoked_at IS NULL
		  AND EXISTS (SELECT 1 FROM memberships m
		              WHERE m.org_id = $2::uuid AND m.user_id = sessions.user_id)
		  AND EXISTS (SELECT 1 FROM organizations o
		              WHERE o.id = $2::uuid AND o.deleted_at IS NULL)`,
		id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrNotMember
	}
	return nil
}

func (s *AccountStore) RevokeSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = NOW() WHERE id = $1::uuid AND revoked_at IS NULL`, id)
	return err
}

func (s *AccountStore) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = NOW() WHERE user_id = $1::uuid AND revoked_at IS NULL`, userID)
	return err
}

// ── invitations ──────────────────────────────────────────────────────────────

func (s *AccountStore) CreateInvitation(ctx context.Context, in storage.Invitation, tokenHash []byte) (*storage.Invitation, error) {
	var invitedBy *string
	if in.InvitedBy != "" {
		invitedBy = &in.InvitedBy
	}
	var out storage.Invitation
	var role string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO invitations (org_id, email, role, token_hash, invited_by, expires_at)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, $6)
		RETURNING id::text, org_id::text, email, role, COALESCE(invited_by::text, ''),
		          created_at, expires_at, accepted_at`,
		in.OrgID, in.Email, string(in.Role), tokenHash, invitedBy, in.ExpiresAt,
	).Scan(&out.ID, &out.OrgID, &out.Email, &role, &out.InvitedBy, &out.CreatedAt,
		&out.ExpiresAt, &out.AcceptedAt)
	if err != nil {
		return nil, fmt.Errorf("create invitation: %w", err)
	}
	out.Role = storage.Role(role)
	return &out, nil
}

// InvitationByToken returns an invitation only while it is usable. Unknown,
// expired and already-accepted all come back as the same error: someone
// guessing tokens must not learn which of those they hit.
func (s *AccountStore) InvitationByToken(ctx context.Context, tokenHash []byte) (*storage.Invitation, error) {
	var out storage.Invitation
	var role string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, org_id::text, email, role, COALESCE(invited_by::text, ''),
		       created_at, expires_at, accepted_at
		FROM invitations
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > NOW()`, tokenHash,
	).Scan(&out.ID, &out.OrgID, &out.Email, &role, &out.InvitedBy, &out.CreatedAt,
		&out.ExpiresAt, &out.AcceptedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ErrInviteInvalid
	}
	if err != nil {
		return nil, err
	}
	out.Role = storage.Role(role)
	return &out, nil
}

// AcceptInvitation marks the invitation used. The WHERE clause is what makes
// it single-use: two registrations racing on one link, only one wins.
func (s *AccountStore) AcceptInvitation(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE invitations SET accepted_at = NOW()
		WHERE id = $1::uuid AND accepted_at IS NULL AND expires_at > NOW()`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrInviteInvalid
	}
	return nil
}

func (s *AccountStore) InvitationsOf(ctx context.Context, orgID string) ([]storage.Invitation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, org_id::text, email, role, COALESCE(invited_by::text, ''),
		       created_at, expires_at, accepted_at
		FROM invitations WHERE org_id = $1::uuid ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []storage.Invitation{}
	for rows.Next() {
		var inv storage.Invitation
		var role string
		if err := rows.Scan(&inv.ID, &inv.OrgID, &inv.Email, &role, &inv.InvitedBy,
			&inv.CreatedAt, &inv.ExpiresAt, &inv.AcceptedAt); err != nil {
			return nil, err
		}
		inv.Role = storage.Role(role)
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *AccountStore) RevokeInvitation(ctx context.Context, orgID, id string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM invitations WHERE id = $1::uuid AND org_id = $2::uuid AND accepted_at IS NULL`,
		id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return storage.ErrInviteInvalid
	}
	return nil
}
