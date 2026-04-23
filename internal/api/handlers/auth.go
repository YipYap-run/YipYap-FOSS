package handlers

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type AuthHandler struct {
	store         store.Store
	jwt           *auth.JWTIssuer
	mailer        *mailer.Mailer
	publicBaseURL string
}

func NewAuthHandler(s store.Store, jwt *auth.JWTIssuer) *AuthHandler {
	return &AuthHandler{store: s, jwt: jwt}
}

func NewAuthHandlerWithBaseURL(s store.Store, jwt *auth.JWTIssuer, publicBaseURL string) *AuthHandler {
	return &AuthHandler{store: s, jwt: jwt, publicBaseURL: publicBaseURL}
}

func NewAuthHandlerFull(s store.Store, jwt *auth.JWTIssuer, m *mailer.Mailer, publicBaseURL string) *AuthHandler {
	return &AuthHandler{store: s, jwt: jwt, mailer: m, publicBaseURL: publicBaseURL}
}

// setSessionCookie writes the JWT as an HttpOnly session cookie.
func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, token string) {
	maxAge := int(h.jwt.TTL().Seconds())
	if maxAge <= 0 {
		maxAge = 86400
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "yipyap_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   strings.HasPrefix(h.publicBaseURL, "https://"),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     "yipyap_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

type registerRequest struct {
	OrgName         string `json:"org_name"`
	Email           string `json:"email"`
	Password        string `json:"password"`
	ConfirmPassword string `json:"confirm_password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string       `json:"token"`
	User  *domain.User `json:"user"`
}

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRe.ReplaceAllString(s, "")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "org"
	}
	return s
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OrgName == "" || req.Email == "" || req.Password == "" {
		errorResponse(w, http.StatusBadRequest, "org_name, email, and password are required")
		return
	}
	if len(req.Password) < 8 {
		errorResponse(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if req.ConfirmPassword != "" && req.ConfirmPassword != req.Password {
		errorResponse(w, http.StatusBadRequest, "passwords do not match")
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{
		ID:        uuid.New().String(),
		Name:      req.OrgName,
		Slug:      slugify(req.OrgName),
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}

	user := &domain.User{
		ID:           uuid.New().String(),
		OrgID:        org.ID,
		Email:        req.Email,
		PasswordHash: passwordHash,
		Role:         domain.RoleOwner,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	err = h.store.Tx(r.Context(), func(tx store.Store) error {
		if err := tx.Orgs().Create(r.Context(), org); err != nil {
			return err
		}
		if err := tx.Users().Create(r.Context(), user); err != nil {
			return err
		}
		if sp, ok := tx.(store.MonitorStateProvider); ok {
			if err := sp.MonitorStates().SeedBuiltins(r.Context(), org.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		errorResponse(w, http.StatusConflict, "org or user already exists")
		return
	}

	// FOSS fallback: no mailer configured => auto-verify and issue session.
	if h.mailer == nil {
		if err := h.store.Users().MarkEmailVerified(r.Context(), user.ID, now); err != nil {
			log.Printf("register: failed to auto-verify user %s: %v", user.ID, err)
		}
		token, err := h.jwt.Issue(user.ID, org.ID, string(user.Role))
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to issue token")
			return
		}
		h.setSessionCookie(w, token)
		jsonResponse(w, http.StatusCreated, authResponse{Token: token, User: user})
		return
	}

	h.sendVerificationEmail(r.Context(), user)
	jsonResponse(w, http.StatusCreated, map[string]string{
		"status": "verification_sent",
		"email":  user.Email,
	})
}

// sendVerificationEmail issues a 24h token, sends the email, and records the send
// (updates sent_at + resend count/window). Best-effort; errors are logged only.
func (h *AuthHandler) sendVerificationEmail(ctx context.Context, user *domain.User) {
	if h.mailer == nil {
		return
	}
	token, err := h.jwt.IssueEmailVerification(user.ID, user.OrgID, user.Email)
	if err != nil {
		log.Printf("verify-email: issue token for %s: %v", user.ID, err)
		return
	}
	verifyURL := h.publicBaseURL + "/verify-email?token=" + token
	subject := "YipYap: Verify your email"
	body := "Welcome to YipYap!\n\n" +
		"Click the link below to verify your email address and activate your account:\n\n" +
		verifyURL + "\n\n" +
		"This link expires in 24 hours.\n\n" +
		"If you did not sign up for YipYap, you can safely ignore this email."
	go func(email, id string) {
		if err := h.mailer.Send(email, subject, body); err != nil {
			log.Printf("verify-email: send to %s: %v", id, err)
		}
	}(user.Email, user.ID)
	if err := h.store.Users().RecordVerificationSend(ctx, user.ID, time.Now().UTC()); err != nil {
		log.Printf("verify-email: record send for %s: %v", user.ID, err)
	}
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		errorResponse(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := h.store.Users().GetByEmail(r.Context(), req.Email)
	if err != nil {
		// User not found  - still run bcrypt to prevent timing leak.
		_ = auth.VerifyPassword("$2a$10$0000000000000000000000uKMCRsMNJOq3hGBEExGSm3LpaFGvleq", req.Password)
		errorResponse(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if user.DisabledAt != nil {
		jsonResponse(w, http.StatusForbidden, map[string]interface{}{
			"account_disabled": true,
		})
		return
	}

	if user.EmailVerifiedAt == nil {
		jsonResponse(w, http.StatusForbidden, map[string]interface{}{
			"email_not_verified": true,
			"email":              user.Email,
		})
		return
	}

	// Check for MFA.
	hasMFA, methods := userHasMFA(r.Context(), h.store, user)
	if hasMFA {
		mfaToken, err := h.jwt.IssueMFA(user.ID, user.OrgID, string(user.Role))
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to issue MFA token")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"mfa_required": true,
			"mfa_token":    mfaToken,
			"mfa_methods":  methods,
		})
		return
	}

	token, err := h.jwt.Issue(user.ID, user.OrgID, string(user.Role))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	h.setSessionCookie(w, token)
	jsonResponse(w, http.StatusOK, authResponse{Token: token, User: user})
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		errorResponse(w, http.StatusUnauthorized, "authentication required")
		return
	}

	user, err := h.store.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	if user.DisabledAt != nil {
		clearSessionCookie(w)
		errorResponse(w, http.StatusUnauthorized, "account is disabled")
		return
	}

	token, err := h.jwt.Issue(user.ID, user.OrgID, string(user.Role))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	h.setSessionCookie(w, token)
	jsonResponse(w, http.StatusOK, authResponse{Token: token, User: user})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

