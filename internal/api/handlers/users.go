package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/accounts"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// SeatUpdater is called when org seats change. Nil in FOSS builds.
type SeatUpdater interface {
	OnSeatAdded(ctx context.Context, orgID string) error
	OnSeatRemoved(ctx context.Context, orgID string) error
}

type UserHandler struct {
	store store.Store
	seats SeatUpdater
	// inviteMailer sends invite emails; nil when SMTP is not configured.
	inviteMailer  InviteMailer
	publicBaseURL string
	// startSession signs a member in after they accept an invite; nil
	// sends them to the login page instead.
	startSession SessionStarter
}

// errCountMembers is returned by checkMemberLimit when the store lookup fails.
var errCountMembers = errors.New("failed to count members")

func NewUserHandler(s store.Store) *UserHandler {
	return &UserHandler{store: s}
}

// SetSeatUpdater wires an optional billing seat updater (non-nil in SaaS builds).
func (h *UserHandler) SetSeatUpdater(su SeatUpdater) {
	h.seats = su
}

// Limits on the member list query string (ux-3).
const (
	userSearchMaxLen = 200
	userIDsMax       = 100
)

// userListResponse is the paged member list. Total counts every member that
// matches the filter, not just this page.
type userListResponse struct {
	Users  []*domain.User `json:"users"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// List returns the org's active members as {users,total,limit,offset}.
// Query parameters: limit (1-100, default 50), offset, q (case-insensitive
// match on email or name), role, and ids (comma-separated user IDs, up to
// 100, for resolving IDs to people in one request).
func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	query := r.URL.Query()
	f := store.UserFilter{ListParams: paginationFromQuery(r)}

	f.Query = strings.TrimSpace(query.Get("q"))
	if len(f.Query) > userSearchMaxLen {
		errorResponse(w, http.StatusBadRequest, "q is too long")
		return
	}
	if role := query.Get("role"); role != "" {
		switch domain.UserRole(role) {
		case domain.RoleOwner, domain.RoleAdmin, domain.RoleMember, domain.RoleViewer:
			f.Role = role
		default:
			errorResponse(w, http.StatusBadRequest, "invalid role")
			return
		}
	}
	if query.Has("ids") {
		ids := []string{}
		seen := map[string]bool{}
		for _, id := range strings.Split(query.Get("ids"), ",") {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			if len(id) > 64 {
				errorResponse(w, http.StatusBadRequest, "invalid user id in ids")
				return
			}
			seen[id] = true
			ids = append(ids, id)
		}
		if len(ids) > userIDsMax {
			errorResponse(w, http.StatusBadRequest, "ids accepts at most 100 user IDs")
			return
		}
		f.IDs = ids
		// A batch lookup returns every requested user on one page.
		f.Limit = userIDsMax
		f.Offset = 0
	}

	users, total, err := h.store.Users().SearchByOrg(r.Context(), claims.OrgID, f)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	if users == nil {
		users = []*domain.User{}
	}
	jsonResponse(w, http.StatusOK, userListResponse{Users: users, Total: total, Limit: f.Limit, Offset: f.Offset})
}

func (h *UserHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	user, err := h.store.Users().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if user.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	jsonResponse(w, http.StatusOK, user)
}

// seatSyncWarning is returned with a member change that succeeded while the
// subscription seat quantity could not be updated. The next member change
// recomputes the quantity from the member count and corrects it.
const seatSyncWarning = "The member change was saved, but the subscription seat count could not be updated. It will be corrected on the next member change; contact support if billing looks wrong."

func (h *UserHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())

	var req struct {
		Email    string          `json:"email"`
		Name     string          `json:"name"`
		Password string          `json:"password"`
		Role     domain.UserRole `json:"role"`
		Phone    string          `json:"phone"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		errorResponse(w, http.StatusBadRequest, "email and password are required")
		return
	}
	if len(req.Password) < 8 {
		errorResponse(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if req.Role == "" {
		req.Role = domain.RoleMember
	}
	switch req.Role {
	case domain.RoleOwner:
		errorResponse(w, http.StatusForbidden, "use ownership transfer to change org owner")
		return
	case domain.RoleAdmin:
		if domain.UserRole(claims.Role) != domain.RoleOwner {
			errorResponse(w, http.StatusForbidden, "only the org owner can create admins")
			return
		}
	case domain.RoleMember, domain.RoleViewer:
		// allowed
	default:
		errorResponse(w, http.StatusBadRequest, "invalid role")
		return
	}

	// Seat limit (plan-based on SaaS, operator-set on self-hosted).
	org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get org")
		return
	}
	if err := checkMemberLimit(r.Context(), h.store, org); err != nil {
		if errors.Is(err, errCountMembers) {
			errorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
		errorResponse(w, http.StatusForbidden, err.Error())
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	user := &domain.User{
		ID:           uuid.New().String(),
		OrgID:        claims.OrgID,
		Email:        req.Email,
		Name:         req.Name,
		PasswordHash: hash,
		Role:         req.Role,
		Phone:        req.Phone,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := h.store.Users().Create(r.Context(), user); err != nil {
		errorResponse(w, http.StatusConflict, "user already exists")
		return
	}
	// Admin-invited accounts are implicitly trusted; skip the email gate.
	if err := h.store.Users().MarkEmailVerified(r.Context(), user.ID, now); err != nil {
		slog.Warn("users.create: failed to mark admin-invited user verified", "user_id", user.ID, "error", err)
	}
	user.EmailVerifiedAt = &now
	if h.seats != nil {
		if err := h.seats.OnSeatAdded(r.Context(), claims.OrgID); err != nil {
			slog.Error("billing: seat add failed", "error", err, "org", claims.OrgID)
		}
	}
	jsonResponse(w, http.StatusCreated, user)
}

func (h *UserHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	user, err := h.store.Users().GetByID(r.Context(), id)
	if err != nil || user.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}

	var req struct {
		Email *string          `json:"email"`
		Name  *string          `json:"name"`
		Role  *domain.UserRole `json:"role"`
		Phone *string          `json:"phone"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Contact details (email, phone) are account-recovery and paging
	// channels. Nobody may change them on another user's row, including the
	// owner's: an admin who could repoint the owner's email could reset the
	// owner's password. Email changes go through PUT /auth/email, which
	// re-checks the caller's password and MFA. Values equal to the current
	// ones are treated as no-ops so clients that echo the full record work.
	isSelf := user.ID == claims.UserID
	if req.Email != nil && !strings.EqualFold(strings.TrimSpace(*req.Email), user.Email) {
		if !isSelf {
			errorResponse(w, http.StatusForbidden, "only the user can change their own email address")
			return
		}
		errorResponse(w, http.StatusForbidden, "change your email address from account settings (PUT /api/v1/auth/email)")
		return
	}
	if req.Phone != nil {
		phone := strings.TrimSpace(*req.Phone)
		if phone != user.Phone {
			if !isSelf {
				errorResponse(w, http.StatusForbidden, "only the user can change their own phone number")
				return
			}
			if err := validateUserPhone(phone); err != nil {
				errorResponse(w, http.StatusBadRequest, err.Error())
				return
			}
			user.Phone = phone
		}
	}
	if req.Name != nil {
		user.Name = *req.Name
	}
	roleChanged := false
	if req.Role != nil {
		target := *req.Role
		callerRole := domain.UserRole(claims.Role)

		if target == domain.RoleOwner {
			errorResponse(w, http.StatusForbidden, "use ownership transfer to change org owner")
			return
		}
		if target != domain.RoleAdmin && target != domain.RoleMember && target != domain.RoleViewer {
			errorResponse(w, http.StatusBadRequest, "invalid role")
			return
		}
		if user.Role == domain.RoleOwner {
			errorResponse(w, http.StatusForbidden, "cannot change the owner's role")
			return
		}
		if target == domain.RoleAdmin && callerRole != domain.RoleOwner {
			errorResponse(w, http.StatusForbidden, "only the org owner can promote to admin")
			return
		}
		roleChanged = user.Role != target
		user.Role = target
	}
	user.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Users().Update(r.Context(), user); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update user")
		return
	}
	// Session tokens carry the role, so a role change signs the user out
	// everywhere rather than leaving tokens with the old role.
	if roleChanged {
		if err := session.RevokeAll(r.Context(), h.store, user.ID, ""); err != nil {
			slog.Error("users.update: revoke sessions after role change", "user_id", user.ID, "error", err)
			errorResponse(w, http.StatusInternalServerError, "role updated, but signing out the user's sessions failed")
			return
		}
	}
	jsonResponse(w, http.StatusOK, user)
}

func (h *UserHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())
	if id == claims.UserID {
		errorResponse(w, http.StatusBadRequest, "cannot delete yourself")
		return
	}
	user, err := h.store.Users().GetByID(r.Context(), id)
	if err != nil || user.OrgID != claims.OrgID || user.DeletedAt != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	if user.Role == domain.RoleOwner {
		errorResponse(w, http.StatusForbidden, "cannot delete the org owner")
		return
	}
	revokeKeys, err := parseRevokeAPIKeys(r)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	var revoked int64
	if revokeKeys {
		n, err := h.store.APIKeys().DeleteByCreator(r.Context(), claims.OrgID, user.ID)
		if err != nil {
			slog.Error("users.delete: revoke api keys", "user_id", user.ID, "error", err)
			errorResponse(w, http.StatusInternalServerError, "failed to revoke api keys")
			return
		}
		revoked = n
	}

	// The row becomes a "Deleted user" tombstone so tickets, incidents and
	// overrides the user wrote stay intact.
	deleteUser := accounts.Delete
	if !revokeKeys {
		deleteUser = accounts.DeleteKeepAPIKeys
	}
	if err := deleteUser(r.Context(), h.store, user.ID, time.Now().UTC()); err != nil {
		slog.Error("users.delete: delete user", "user_id", user.ID, "error", err)
		if !errors.Is(err, accounts.ErrSessionRevoke) {
			errorResponse(w, http.StatusInternalServerError, "failed to delete user")
			return
		}
		// The row is already anonymized and disabled, so refresh and every
		// sign-in path refuse it. Finish the seat update below.
	}
	resp := map[string]interface{}{}
	if revokeKeys {
		resp["revoked_api_keys"] = revoked
	}
	if h.seats != nil {
		if err := h.seats.OnSeatRemoved(r.Context(), claims.OrgID); err != nil {
			slog.Error("billing: seat remove failed", "error", err, "org", claims.OrgID)
			resp["billing_warning"] = seatSyncWarning
		}
	}
	if len(resp) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	jsonResponse(w, http.StatusOK, resp)
}

func (h *UserHandler) TransferOwnership(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims.Role != string(domain.RoleOwner) {
		errorResponse(w, http.StatusForbidden, "only the org owner can transfer ownership")
		return
	}

	var req struct {
		UserID          string `json:"user_id"`
		CurrentPassword string `json:"current_password"`
		mfaProof
	}
	if err := decodeBody(r, &req); err != nil || req.UserID == "" {
		errorResponse(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if req.CurrentPassword == "" {
		errorResponse(w, http.StatusBadRequest, "current_password is required")
		return
	}
	if req.UserID == claims.UserID {
		errorResponse(w, http.StatusBadRequest, "cannot transfer ownership to yourself")
		return
	}

	ctx := r.Context()

	// Verify caller's password and MFA before proceeding.
	caller, err := h.store.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load user")
		return
	}
	// Step-up failures are 403 with a code, not 401: the session is fine,
	// and the SPA treats a 401 as "signed out" and leaves the page (org-3).
	if err := auth.VerifyPassword(caller.PasswordHash, req.CurrentPassword); err != nil {
		transferStepUpError(w, "invalid_password", "current password is incorrect")
		return
	}
	if hasMFA, _, _ := userHasMFA(ctx, h.store, caller); hasMFA && !req.present() {
		transferStepUpError(w, "mfa_required", "enter a code from your authenticator app or a backup code")
		return
	}
	if err := verifyMFAIfEnabled(ctx, h.store, caller, req.mfaProof); err != nil {
		transferStepUpError(w, "mfa_invalid", "that authentication code is not valid")
		return
	}

	target, err := h.store.Users().GetByID(ctx, req.UserID)
	if err != nil || target.OrgID != claims.OrgID || target.DisabledAt != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	if target.Role != domain.RoleAdmin {
		errorResponse(w, http.StatusBadRequest, "target user must be an admin before ownership can be transferred")
		return
	}

	err = h.store.Tx(ctx, func(tx store.Store) error {
		target.Role = domain.RoleOwner
		target.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		if err := tx.Users().Update(ctx, target); err != nil {
			return err
		}
		caller, err := tx.Users().GetByID(ctx, claims.UserID)
		if err != nil {
			return err
		}
		caller.Role = domain.RoleAdmin
		caller.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		return tx.Users().Update(ctx, caller)
	})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to transfer ownership")
		return
	}
	// Both roles changed. The caller's own token still says owner, so the
	// caller is signed out too and signs back in as admin.
	for _, id := range []string{target.ID, claims.UserID} {
		if err := session.RevokeAll(ctx, h.store, id, ""); err != nil {
			slog.Error("transfer ownership: revoke sessions", "user_id", id, "error", err)
		}
	}
	clearSessionCookie(w)

	jsonResponse(w, http.StatusOK, map[string]string{
		"message":   "ownership transferred",
		"new_owner": target.Email,
	})
}

func transferStepUpError(w http.ResponseWriter, code, message string) {
	jsonResponse(w, http.StatusForbidden, map[string]string{"error": message, "code": code})
}

// validateUserPhone checks a user's contact phone number. Empty clears it.
// Otherwise it must hold 7 to 15 digits (E.164 length), may start with "+",
// and may use spaces, dots, dashes and parentheses as separators.
func validateUserPhone(phone string) error {
	if phone == "" {
		return nil
	}
	if len(phone) > 32 {
		return errors.New("phone number is too long")
	}
	digits := 0
	for i, r := range phone {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '+' && i == 0:
		case r == ' ' || r == '-' || r == '.' || r == '(' || r == ')':
		default:
			return errors.New("phone number may contain only digits, a leading +, spaces, dots, dashes and parentheses")
		}
	}
	if digits < 7 || digits > 15 {
		return errors.New("phone number must have between 7 and 15 digits")
	}
	return nil
}
