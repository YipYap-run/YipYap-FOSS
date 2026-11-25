package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Member invites replace admin-chosen passwords. An owner or admin invites
// an email address with a role; the invitee opens the accept link and sets
// their own password. The link token is "<invite id>.<secret>"; only the
// SHA-256 of the secret is stored, and it is compared in constant time.

// InviteMailer sends invite emails. *mailer.Mailer satisfies it.
type InviteMailer interface {
	Send(to, subject, body string) error
}

const (
	// passwordMinLen and passwordMaxLen bound a user-chosen password. bcrypt
	// rejects input longer than 72 bytes.
	passwordMinLen = 8
	passwordMaxLen = 72
	inviteNameMax  = 200
	inviteTokenMax = 256
	// acceptInvitePath is the SPA route the accept link opens.
	acceptInvitePath = "/invite/accept"
)

// SessionStarter signs a user in: it issues a session token and sets the
// session cookie on w. (*AuthHandler).StartSession satisfies it.
type SessionStarter func(w http.ResponseWriter, user *domain.User) (string, error)

// SetSessionStarter lets AcceptInvite sign the new member in. Without it the
// accept response tells the invitee to sign in.
func (h *UserHandler) SetSessionStarter(fn SessionStarter) {
	h.startSession = fn
}

// SetInviteDelivery configures how invite links reach the invitee. With a
// nil mailer (SMTP not configured) the create and resend responses return
// the accept link for the admin to share instead.
func (h *UserHandler) SetInviteDelivery(m InviteMailer, publicBaseURL string) {
	if mm, ok := m.(*mailer.Mailer); ok && mm == nil {
		m = nil
	}
	h.inviteMailer = m
	h.publicBaseURL = strings.TrimRight(publicBaseURL, "/")
}

// LegacyCreate answers the removed POST /users endpoint. Admins no longer
// choose a password for someone else; they send an invite.
func (h *UserHandler) LegacyCreate(w http.ResponseWriter, _ *http.Request) {
	errorResponse(w, http.StatusGone, "adding a member with a password is no longer supported. Send an invite with POST /api/v1/users/invites")
}

// inviteView is an invite as returned to owners and admins.
type inviteView struct {
	*domain.OrgInvite
	Expired       bool   `json:"expired"`
	InvitedByName string `json:"invited_by_name,omitempty"`
}

// inviteDelivery is the create and resend response.
type inviteDelivery struct {
	Invite    inviteView `json:"invite"`
	EmailSent bool       `json:"email_sent"`
	// AcceptURL is set only when SMTP is not configured, so the admin can
	// pass the link on. It is never returned when the invite was emailed.
	AcceptURL string `json:"accept_url,omitempty"`
	Warning   string `json:"warning,omitempty"`
}

// ListInvites handles GET /users/invites: the org's pending invites,
// expired ones included so they can be resent.
func (h *UserHandler) ListInvites(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	invites, err := h.store.Invites().ListPending(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list invites")
		return
	}
	now := time.Now().UTC()
	names := map[string]string{}
	out := make([]inviteView, 0, len(invites))
	for _, inv := range invites {
		out = append(out, h.viewInvite(r.Context(), inv, now, names))
	}
	jsonResponse(w, http.StatusOK, out)
}

// CreateInvite handles POST /users/invites.
func (h *UserHandler) CreateInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)

	var req struct {
		Email string          `json:"email"`
		Role  domain.UserRole `json:"role"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email, msg := normalizeInviteEmail(req.Email)
	if msg != "" {
		errorResponse(w, http.StatusBadRequest, msg)
		return
	}
	if req.Role == "" {
		req.Role = domain.RoleMember
	}
	if status, msg := checkInviteRole(req.Role, domain.UserRole(claims.Role)); status != 0 {
		errorResponse(w, status, msg)
		return
	}

	org, err := h.store.Orgs().GetByID(ctx, claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get org")
		return
	}
	if u, err := h.store.Users().GetByEmail(ctx, email); err == nil && u != nil {
		if u.OrgID == claims.OrgID {
			errorResponse(w, http.StatusConflict, "this person is already a member of the organization")
			return
		}
		errorResponse(w, http.StatusConflict, "this email already has a YipYap account, and an account can belong to one organization only")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	if existing, err := h.store.Invites().GetPendingByEmail(ctx, claims.OrgID, email); err == nil {
		if !existing.Expired(now) {
			errorResponse(w, http.StatusConflict, "an invite is already pending for this email. Resend it from the invite list instead")
			return
		}
		// An expired invite only blocks the email; replace it.
		if err := h.store.Invites().Revoke(ctx, existing.ID, now); err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to replace the expired invite")
			return
		}
	}

	if org.Plan == domain.PlanFree {
		members, err := h.store.Users().CountByOrg(ctx, claims.OrgID)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to count members")
			return
		}
		pending, err := h.store.Invites().CountActive(ctx, claims.OrgID, now)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to count invites")
			return
		}
		if members+pending >= domain.FreeMaxMembers {
			errorResponse(w, http.StatusForbidden, fmt.Sprintf("the Free plan allows %d members, counting pending invites. Upgrade, or revoke a pending invite, to invite more", domain.FreeMaxMembers))
			return
		}
	}

	token, hash, err := newInviteSecret()
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create invite")
		return
	}
	inv := &domain.OrgInvite{
		ID:        uuid.New().String(),
		OrgID:     claims.OrgID,
		Email:     email,
		Role:      req.Role,
		TokenHash: hash,
		InvitedBy: claims.UserID,
		ExpiresAt: now.Add(domain.InviteTTL),
		CreatedAt: now,
	}
	if err := h.store.Invites().Create(ctx, inv); err != nil {
		// The unique pending index lost a race with a parallel request.
		errorResponse(w, http.StatusConflict, "an invite is already pending for this email. Resend it from the invite list instead")
		return
	}
	jsonResponse(w, http.StatusCreated, h.deliverInvite(ctx, org, inv, inv.ID+"."+token, claims.UserID, now))
}

// ResendInvite handles POST /users/invites/{id}/resend. It issues a new
// link (the old one stops working) and restarts the 7-day window.
func (h *UserHandler) ResendInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	claims := middleware.GetClaims(ctx)
	inv, ok := h.orgInvite(w, r, claims.OrgID)
	if !ok {
		return
	}
	if inv.Role == domain.RoleAdmin && domain.UserRole(claims.Role) != domain.RoleOwner {
		errorResponse(w, http.StatusForbidden, "only the org owner can resend an admin invite")
		return
	}
	org, err := h.store.Orgs().GetByID(ctx, claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get org")
		return
	}
	token, hash, err := newInviteSecret()
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to resend invite")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	expires := now.Add(domain.InviteTTL)
	if err := h.store.Invites().Rotate(ctx, inv.ID, hash, expires); err != nil {
		errorResponse(w, http.StatusConflict, "this invite is no longer pending")
		return
	}
	inv.TokenHash = hash
	inv.ExpiresAt = expires
	jsonResponse(w, http.StatusOK, h.deliverInvite(ctx, org, inv, inv.ID+"."+token, claims.UserID, now))
}

// RevokeInvite handles DELETE /users/invites/{id}.
func (h *UserHandler) RevokeInvite(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	inv, ok := h.orgInvite(w, r, claims.OrgID)
	if !ok {
		return
	}
	if err := h.store.Invites().Revoke(r.Context(), inv.ID, time.Now().UTC().Truncate(time.Second)); err != nil {
		errorResponse(w, http.StatusConflict, "this invite is no longer pending")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GetInviteForAccept handles the public GET /invites/accept?token=. It
// describes a valid invite so the accept page can show who is joining what.
func (h *UserHandler) GetInviteForAccept(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	inv, _, ok := h.validInvite(ctx, w, r.URL.Query().Get("token"))
	if !ok {
		return
	}
	org, err := h.store.Orgs().GetByID(ctx, inv.OrgID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "this invite link is not valid")
		return
	}
	_, lookupErr := h.store.Users().GetByEmail(ctx, inv.Email)
	jsonResponse(w, http.StatusOK, map[string]any{
		"email":          inv.Email,
		"role":           inv.Role,
		"org_name":       org.Name,
		"expires_at":     inv.ExpiresAt,
		"account_exists": lookupErr == nil,
	})
}

var (
	errInviteClaimed  = errors.New("invite already claimed")
	errInviteAccount  = errors.New("account exists")
	errInviteSeatsMax = errors.New("member limit reached")
)

// AcceptInvite handles the public POST /invites/accept. It creates the
// invitee's account with the password they chose and marks the invite
// accepted in one transaction, so a token works once.
func (h *UserHandler) AcceptInvite(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	var req struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) > inviteNameMax {
		errorResponse(w, http.StatusBadRequest, fmt.Sprintf("name must be at most %d characters", inviteNameMax))
		return
	}
	if msg := checkNewPassword(req.Password); msg != "" {
		errorResponse(w, http.StatusBadRequest, msg)
		return
	}
	inv, hash, ok := h.validInvite(ctx, w, req.Token)
	if !ok {
		return
	}
	if _, err := h.store.Users().GetByEmail(ctx, inv.Email); err == nil {
		writeInviteAccountExists(w)
		return
	}
	pwHash, err := auth.HashPassword(req.Password)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	user := &domain.User{
		ID:           uuid.New().String(),
		OrgID:        inv.OrgID,
		Email:        inv.Email,
		Name:         req.Name,
		PasswordHash: pwHash,
		Role:         inv.Role,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	err = h.store.Tx(ctx, func(tx store.Store) error {
		org, err := tx.Orgs().GetByID(ctx, inv.OrgID)
		if err != nil {
			return err
		}
		if org.Plan == domain.PlanFree {
			n, err := tx.Users().CountByOrg(ctx, inv.OrgID)
			if err != nil {
				return err
			}
			if n >= domain.FreeMaxMembers {
				return errInviteSeatsMax
			}
		}
		claimed, err := tx.Invites().MarkAccepted(ctx, inv.ID, hash, now)
		if err != nil {
			return err
		}
		if !claimed {
			return errInviteClaimed
		}
		if err := tx.Users().Create(ctx, user); err != nil {
			if isUniqueViolation(err) {
				return errInviteAccount
			}
			return err
		}
		// Following the link shows the invitee controls the mailbox (or,
		// without SMTP, that an admin vouched for them by sharing it).
		return tx.Users().MarkEmailVerified(ctx, user.ID, now)
	})
	switch {
	case errors.Is(err, errInviteClaimed):
		errorResponse(w, http.StatusConflict, "this invite is no longer valid. Ask an admin of the organization for a new one")
		return
	case errors.Is(err, errInviteAccount):
		writeInviteAccountExists(w)
		return
	case errors.Is(err, errInviteSeatsMax):
		errorResponse(w, http.StatusForbidden, "this organization has reached its member limit. Ask an admin to upgrade the plan, then open the link again")
		return
	case err != nil:
		slog.Error("invites.accept: failed", "invite_id", inv.ID, "error", err)
		errorResponse(w, http.StatusInternalServerError, "failed to accept invite")
		return
	}

	if h.seats != nil {
		if err := h.seats.OnSeatAdded(ctx, inv.OrgID); err != nil {
			// The invitee cannot act on this; the next member change
			// recomputes the quantity from the member count.
			slog.Error("billing: seat add failed after invite accept", "error", err, "org", inv.OrgID)
		}
	}
	resp := map[string]any{
		"status":    "accepted",
		"email":     user.Email,
		"signed_in": false,
	}
	if h.startSession != nil {
		// The account exists either way; a failed sign-in only sends the
		// invitee to the login page.
		if token, err := h.startSession(w, user); err != nil {
			slog.Error("invites.accept: failed to start session", "invite_id", inv.ID, "error", err)
		} else {
			resp["signed_in"] = true
			resp["token"] = token
			resp["user"] = user
		}
	}
	jsonResponse(w, http.StatusCreated, resp)
}

func writeInviteAccountExists(w http.ResponseWriter) {
	jsonResponse(w, http.StatusConflict, map[string]string{
		"error": "an account with this email already exists, and an account can belong to one organization only. Sign in with that account, or ask the admin to invite a different email",
		"code":  "account_exists",
	})
}

// validInvite resolves an accept token to a pending, unexpired invite and
// the stored hash it matched. On failure it writes the response.
func (h *UserHandler) validInvite(ctx context.Context, w http.ResponseWriter, token string) (*domain.OrgInvite, string, bool) {
	const invalid = "this invite link is not valid"
	id, secret, ok := splitInviteToken(token)
	if !ok {
		errorResponse(w, http.StatusNotFound, invalid)
		return nil, "", false
	}
	inv, err := h.store.Invites().GetByID(ctx, id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, invalid)
		return nil, "", false
	}
	hash := hashInviteSecret(secret)
	if subtle.ConstantTimeCompare([]byte(hash), []byte(inv.TokenHash)) != 1 {
		errorResponse(w, http.StatusNotFound, invalid)
		return nil, "", false
	}
	switch {
	case inv.AcceptedAt != nil:
		errorResponse(w, http.StatusGone, "this invite was already used. Sign in instead")
		return nil, "", false
	case inv.RevokedAt != nil:
		errorResponse(w, http.StatusGone, "this invite was revoked. Ask an admin of the organization for a new one")
		return nil, "", false
	case inv.Expired(time.Now().UTC()):
		errorResponse(w, http.StatusGone, "this invite has expired. Ask an admin of the organization to resend it")
		return nil, "", false
	}
	return inv, inv.TokenHash, true
}

// orgInvite loads the {id} invite and checks that it belongs to orgID and
// is pending. On failure it writes the response.
func (h *UserHandler) orgInvite(w http.ResponseWriter, r *http.Request, orgID string) (*domain.OrgInvite, bool) {
	inv, err := h.store.Invites().GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil || inv.OrgID != orgID {
		errorResponse(w, http.StatusNotFound, "invite not found")
		return nil, false
	}
	if !inv.Pending() {
		errorResponse(w, http.StatusConflict, "this invite is no longer pending")
		return nil, false
	}
	return inv, true
}

// deliverInvite emails the accept link, or returns it in the response when
// SMTP is not configured.
func (h *UserHandler) deliverInvite(ctx context.Context, org *domain.Org, inv *domain.OrgInvite, token, inviterID string, now time.Time) inviteDelivery {
	names := map[string]string{}
	out := inviteDelivery{Invite: h.viewInvite(ctx, inv, now, names)}
	link := h.publicBaseURL + acceptInvitePath + "?token=" + url.QueryEscape(token)
	if h.inviteMailer == nil {
		out.AcceptURL = link
		return out
	}
	inviter := h.inviterName(ctx, inviterID, names)
	if inviter == "" {
		inviter = "An admin"
	}
	subject := fmt.Sprintf("You're invited to join %s on YipYap", org.Name)
	body := fmt.Sprintf("%s invited you to join %s on YipYap as %s.\n\n"+
		"Open this link to set your password and join:\n\n%s\n\n"+
		"The link expires on %s (7 days from now). If it expires, ask an admin of %s to resend the invite.\n\n"+
		"If you were not expecting this, you can ignore this email.",
		inviter, org.Name, roleWithArticle(inv.Role), link,
		inv.ExpiresAt.UTC().Format("2 January 2006 at 15:04 UTC"), org.Name)
	if err := h.inviteMailer.Send(inv.Email, subject, body); err != nil {
		slog.Error("invites: failed to send invite email", "invite_id", inv.ID, "error", err)
		out.Warning = "The invite was saved, but the email could not be sent. Try Resend in a few minutes."
		return out
	}
	out.EmailSent = true
	return out
}

func (h *UserHandler) viewInvite(ctx context.Context, inv *domain.OrgInvite, now time.Time, names map[string]string) inviteView {
	return inviteView{
		OrgInvite:     inv,
		Expired:       inv.Expired(now),
		InvitedByName: h.inviterName(ctx, inv.InvitedBy, names),
	}
}

// inviterName returns a display name for a user ID, caching lookups in
// names. It returns "" for a user that no longer exists.
func (h *UserHandler) inviterName(ctx context.Context, userID string, names map[string]string) string {
	if userID == "" {
		return ""
	}
	if n, ok := names[userID]; ok {
		return n
	}
	n := ""
	if u, err := h.store.Users().GetByID(ctx, userID); err == nil {
		n = u.Name
		if n == "" {
			n = u.Email
		}
	}
	names[userID] = n
	return n
}

// checkInviteRole validates the role on a new invite. It returns a zero
// status when the role is allowed.
func checkInviteRole(role, caller domain.UserRole) (int, string) {
	switch role {
	case domain.RoleOwner:
		return http.StatusBadRequest, "the owner role cannot be assigned by invite. Use ownership transfer after the person joins"
	case domain.RoleAdmin:
		if caller != domain.RoleOwner {
			return http.StatusForbidden, "only the org owner can invite admins"
		}
	case domain.RoleMember, domain.RoleViewer:
	default:
		return http.StatusBadRequest, "role must be admin, member or viewer"
	}
	return 0, ""
}

// normalizeInviteEmail trims and validates an invite address. The case is
// kept as typed, since sign-in matches the stored email exactly; pending
// invites are matched case-insensitively.
func normalizeInviteEmail(raw string) (string, string) {
	email := domain.NormalizeEmail(raw)
	if email == "" {
		return "", "email is required"
	}
	if len(email) > 254 {
		return "", "email address is too long"
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return "", "enter a single email address, such as name@example.com"
	}
	return email, ""
}

// checkNewPassword enforces the server-side password bounds.
func checkNewPassword(pw string) string {
	if len(pw) < passwordMinLen {
		return fmt.Sprintf("password must be at least %d characters", passwordMinLen)
	}
	if len(pw) > passwordMaxLen {
		return fmt.Sprintf("password must be at most %d bytes", passwordMaxLen)
	}
	return ""
}

func roleWithArticle(r domain.UserRole) string {
	switch r {
	case domain.RoleAdmin:
		return "an admin"
	case domain.RoleViewer:
		return "a viewer"
	default:
		return "a member"
	}
}

// newInviteSecret returns a random link secret and its stored hash.
func newInviteSecret() (secret, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	secret = base64.RawURLEncoding.EncodeToString(b)
	return secret, hashInviteSecret(secret), nil
}

func hashInviteSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// isUniqueViolation reports whether err is a unique constraint failure on
// SQLite or Postgres.
func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

// splitInviteToken splits "<invite id>.<secret>".
func splitInviteToken(token string) (id, secret string, ok bool) {
	if token == "" || len(token) > inviteTokenMax {
		return "", "", false
	}
	id, secret, ok = strings.Cut(token, ".")
	if !ok || id == "" || secret == "" {
		return "", "", false
	}
	return id, secret, true
}
