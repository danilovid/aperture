package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mutegate/mutegate/internal/auth"
	"github.com/mutegate/mutegate/internal/storage"
)

// Open registration: somebody arrives, gives an address and a password, and
// leaves with an account and an organization of their own, as its owner. It is
// the operator's choice (REGISTRATION_OPEN) and off by default — a gateway
// that holds a company's provider keys is invitation-only until somebody
// decides it is not.
//
// There is no confirmation mail, because there is no mail. What that costs is
// that an address is taken on the say-so of whoever typed it; what it does not
// cost is anybody else's data, because the newcomer lands in an organization
// nobody else is in.

// handleSignup creates an account, an organization for it, and a session.
func (h *Handlers) handleSignup(w http.ResponseWriter, r *http.Request) {
	if h.AccountStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts are not configured"})
		return
	}
	if !h.registrationOpen {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": "registration on this installation is by invitation; ask an admin of your organization for a link",
		})
		return
	}
	var req struct {
		Email           string `json:"email"`
		Password        string `json:"password"`
		PasswordConfirm string `json:"password_confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
		return
	}
	email := auth.NormalizeEmail(req.Email)
	if !auth.ValidEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "that does not look like an email address"})
		return
	}
	// The form checks this too; the API is not only called by the form.
	if req.Password != req.PasswordConfirm {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "the passwords do not match"})
		return
	}

	// Counted before the password is hashed: hashing is the expensive part,
	// and an attempt is an attempt whether or not the address was free.
	limitKey := clientIP(r)
	if h.signups.blocked(limitKey) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "too many sign-ups from this address; try again later",
		})
		return
	}
	h.signups.fail(limitKey)

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	user, err := h.AccountStore.CreateUser(r.Context(), email, "", hash)
	if err != nil {
		if errors.Is(err, storage.ErrEmailTaken) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "an account with this address already exists — sign in instead",
			})
			return
		}
		h.Logger.Error("sign-up: create user failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not create the account"})
		return
	}

	org, err := h.createOwnOrganization(r.Context(), email)
	if err == nil {
		err = h.AccountStore.AddMember(r.Context(), org.ID, user.ID, storage.RoleOwner)
	}
	if err != nil {
		// The account exists and can sign in; it just has nowhere to be yet,
		// which is the state somebody waiting for an invitation is in too.
		h.Logger.Error("sign-up: organization for a new account failed", "err", err, "user", email)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "the account was created but its organization was not; sign in and ask for an invitation",
		})
		return
	}

	who := userActor(user)
	h.auditAs(r, who, org.ID, "organization.create", org.Slug, map[string]any{"name": org.Name, "via": "sign-up"})
	h.auditAs(r, who, org.ID, "member.join", email, map[string]any{"role": storage.RoleOwner})
	h.Logger.Info("signed up", "user", email, "org", org.Slug)

	_ = h.AccountStore.MarkLogin(r.Context(), user.ID)
	h.startSession(w, r, user, org.ID)
}

// createOwnOrganization makes the organization a new account owns. It is named
// after the address — "ann" for ann@acme.test — because asking for a name is
// one more field between somebody and a working console, and renaming is one
// click later. Slugs are unique across the installation, so a taken one gets a
// number, and after a few of those a random tail.
func (h *Handlers) createOwnOrganization(ctx context.Context, email string) (*storage.Organization, error) {
	name, _, _ := strings.Cut(email, "@")
	base := slugify(name)
	if len(base) > 32 {
		base = strings.Trim(base[:32], "-")
	}
	if base == "" {
		base = "org"
	}
	for i := 1; i <= 12; i++ {
		slug := base
		switch {
		case i > 1 && i <= 9:
			slug = base + "-" + strconv.Itoa(i)
		case i > 9:
			tail := make([]byte, 3)
			if _, err := rand.Read(tail); err != nil {
				return nil, err
			}
			slug = base + "-" + hex.EncodeToString(tail)
		}
		org, err := h.AccountStore.CreateOrganization(ctx, name, slug)
		if errors.Is(err, storage.ErrSlugTaken) {
			continue
		}
		return org, err
	}
	return nil, errors.New("no free slug for " + base)
}
