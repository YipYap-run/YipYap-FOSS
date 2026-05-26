package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// SeatUpdater is called when org seats change. Nil in FOSS builds.
type SeatUpdater interface {
	OnSeatAdded(ctx context.Context, orgID string) error
	OnSeatRemoved(ctx context.Context, orgID string) error
}

type UserHandler struct {
	store store.Store
	seats SeatUpdater
}

func NewUserHandler(s store.Store) *UserHandler {
	return &UserHandler{store: s}
}

// SetSeatUpdater wires an optional billing seat updater (non-nil in SaaS builds).
func (h *UserHandler) SetSeatUpdater(su SeatUpdater) {
	h.seats = su
}

func (h *UserHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	users, err := h.store.Users().ListByOrg(r.Context(), claims.OrgID, params)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list users")
		return
	}
	jsonResponse(w, http.StatusOK, users)
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

	// Free tier seat limit.
	org, err := h.store.Orgs().GetByID(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get org")
		return
	}
	if org.Plan == domain.PlanFree {
		members, err := h.store.Users().ListByOrg(r.Context(), claims.OrgID, store.ListParams{Limit: 100})
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to count members")
			return
		}
		if len(members) >= domain.FreeMaxMembers {
			errorResponse(w, http.StatusForbidden, "free plan limited to 5 members. Upgrade to add more")
			return
		}
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
		CreatedAt: now,
		UpdatedAt: now,
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
	if req.Email != nil {
		user.Email = *req.Email
	}
	if req.Name != nil {
		user.Name = *req.Name
	}
	if req.Role != nil {
		target := *req.Role
		callerRole := domain.UserRole(claims.Role)

		if target == domain.RoleOwner {
			errorResponse(w, http.StatusForbidden, "use ownership transfer to change org owner")
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
		user.Role = target
	}
	if req.Phone != nil {
		user.Phone = *req.Phone
	}
	user.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Users().Update(r.Context(), user); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update user")
		return
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
	if err != nil || user.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	if user.Role == domain.RoleOwner {
		errorResponse(w, http.StatusForbidden, "cannot delete the org owner")
		return
	}

	if err := h.store.Users().Delete(r.Context(), user.ID); err != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	if h.seats != nil {
		if err := h.seats.OnSeatRemoved(r.Context(), claims.OrgID); err != nil {
			slog.Error("billing: seat remove failed", "error", err, "org", claims.OrgID)
		}
	}
	w.WriteHeader(http.StatusNoContent)
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
		MFACode         string `json:"mfa_code"`
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
	if err := auth.VerifyPassword(caller.PasswordHash, req.CurrentPassword); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid current password")
		return
	}
	if err := verifyMFAIfEnabled(ctx, h.store, caller, req.MFACode); err != nil {
		errorResponse(w, http.StatusUnauthorized, "MFA verification failed")
		return
	}

	target, err := h.store.Users().GetByID(ctx, req.UserID)
	if err != nil || target.OrgID != claims.OrgID {
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

	jsonResponse(w, http.StatusOK, map[string]string{
		"message":   "ownership transferred",
		"new_owner": target.Email,
	})
}
