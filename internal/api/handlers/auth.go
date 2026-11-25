package handlers

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

type AuthHandler struct {
	store         store.Store
	jwt           *auth.JWTIssuer
	mailer        *mailer.Mailer
	publicBaseURL string
	// sendMail replaces mailer.Send in tests that inspect outgoing mail.
	sendMail func(to, subject, body string) error
}

// send delivers one email through the test hook or the mailer.
func (h *AuthHandler) send(to, subject, body string) error {
	if h.sendMail != nil {
		return h.sendMail(to, subject, body)
	}
	return h.mailer.Send(to, subject, body)
}

// errSlugTaken makes the registration loop retry with a suffixed slug.
var errSlugTaken = errors.New("org slug taken")

// writeEmailTaken sends the 409 for a sign-up whose email has an account.
func writeEmailTaken(w http.ResponseWriter) {
	jsonResponse(w, http.StatusConflict, map[string]string{
		"error": emailTakenMessage,
		"code":  codeEmailTaken,
	})
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
func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	writeSessionCookie(w, r, h.jwt, h.publicBaseURL, token)
}

// StartSession issues a session token for user and sets the session cookie.
// The invite accept handler uses it to sign a new member in.
func (h *AuthHandler) StartSession(w http.ResponseWriter, user *domain.User) (string, error) {
	token, err := h.jwt.Issue(user.ID, user.OrgID, string(user.Role))
	if err != nil {
		return "", err
	}
	writeSessionCookie(w, nil, h.jwt, h.publicBaseURL, token)
	return token, nil
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

// errEmailTaken aborts a registration transaction when the email already
// has an account.
var errEmailTaken = errors.New("email already registered")

// slugAttempts bounds the retries when a generated org slug collides.
const slugAttempts = 5

// slugCandidate returns base for the first attempt and base plus a random
// suffix after that, so two orgs with the same name get distinct slugs.
func slugCandidate(base string, attempt int) string {
	if attempt == 0 {
		return base
	}
	suffix, err := randomHex(3)
	if err != nil {
		suffix = uuid.New().String()[:6]
	}
	return base + "-" + suffix
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.OrgName = strings.TrimSpace(req.OrgName)
	if req.OrgName == "" || strings.TrimSpace(req.Email) == "" || req.Password == "" {
		errorResponse(w, http.StatusBadRequest, "org_name, email, and password are required")
		return
	}
	email, ok := normalizedEmail(w, req.Email)
	if !ok {
		return
	}
	if !checkPasswordPolicy(w, req.Password) {
		return
	}
	if req.ConfirmPassword != "" && req.ConfirmPassword != req.Password {
		errorResponse(w, http.StatusBadRequest, "passwords do not match")
		return
	}

	ctx := r.Context()
	if emailInUse(ctx, h.store, email, "") {
		writeEmailTaken(w)
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	baseSlug := slugify(req.OrgName)
	org := &domain.Org{
		ID:        uuid.New().String(),
		Name:      req.OrgName,
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}

	user := &domain.User{
		ID:           uuid.New().String(),
		OrgID:        org.ID,
		Email:        email,
		PasswordHash: passwordHash,
		Role:         domain.RoleOwner,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	// The slug is globally unique but the org name is not (auth-7): a taken
	// slug gets a random suffix. The email and slug checks run inside the
	// transaction; a unique-index failure from a concurrent sign-up is
	// retried, and then reported as a taken email if that is the cause.
	for attempt := 0; ; attempt++ {
		org.Slug = slugCandidate(baseSlug, attempt)
		err = h.store.Tx(ctx, func(tx store.Store) error {
			if emailInUse(ctx, tx, email, "") {
				return errEmailTaken
			}
			if _, err := tx.Orgs().GetBySlug(ctx, org.Slug); err == nil {
				return errSlugTaken
			}
			if err := tx.Orgs().Create(ctx, org); err != nil {
				return err
			}
			if err := tx.Users().Create(ctx, user); err != nil {
				return err
			}
			if sp, ok := tx.(store.MonitorStateProvider); ok {
				if err := sp.MonitorStates().SeedBuiltins(ctx, org.ID); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			break
		}
		if errors.Is(err, errEmailTaken) || emailInUse(ctx, h.store, email, "") {
			writeEmailTaken(w)
			return
		}
		if attempt+1 >= slugAttempts {
			slog.Error("register: transaction failed", "attempts", attempt+1, "error", err)
			errorResponse(w, http.StatusInternalServerError, "failed to create account, try again")
			return
		}
	}

	// FOSS fallback: no mailer configured => auto-verify and issue session.
	if h.mailer == nil {
		if err := h.store.Users().MarkEmailVerified(r.Context(), user.ID, now); err != nil {
			log.Printf("register: failed to auto-verify user %s: %v", user.ID, err)
		}
		token, err := issueLoginSession(r, h.store, h.jwt, user, auth.AMRPassword)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to issue token")
			return
		}
		h.setSessionCookie(w, r, token)
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
	req.Email = domain.NormalizeEmail(req.Email)
	if req.Email == "" || req.Password == "" {
		errorResponse(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := h.store.Users().GetByEmail(r.Context(), req.Email)
	if err != nil {
		// User not found  - still run bcrypt to prevent timing leak.
		_ = auth.DummyVerify(req.Password)
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

	// An org that enforces SSO refuses password sign-in for members. This
	// runs after the password check so the refusal reveals nothing to
	// someone who does not know the password. An edition that supports a
	// second factor may answer the request here instead of continuing.
	handled, err := h.beginSecondFactorLogin(w, r, user)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to start sign-in")
		return
	}
	if handled {
		return
	}

	token, err := issueLoginSession(r, h.store, h.jwt, user, auth.AMRPassword)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	h.setSessionCookie(w, r, token)
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

	// Once the org enforces SSO, a password session of a member is not
	// renewed. Turning enforcement on already revokes these; this covers a
	// member who signed in as the owner and was later demoted.
	if ssoSessionBlocked(r.Context(), h.store, user, claims.AMR) {
		if err := session.Revoke(r.Context(), h.store, user.ID, claims.SID); err != nil && !errors.Is(err, store.ErrSessionNotFound) {
			slog.Error("refresh: revoke session", "user_id", user.ID, "error", err)
		}
		clearSessionCookie(w)
		jsonResponse(w, http.StatusUnauthorized, map[string]string{
			"error": "your organization requires single sign-on, sign in with SSO",
			"code":  "sso_required",
		})
		return
	}

	token, err := session.Renew(r.Context(), h.store, h.jwt, claims, string(user.Role))
	if errors.Is(err, session.ErrRevoked) {
		clearSessionCookie(w)
		errorResponse(w, http.StatusUnauthorized, "session expired or signed out")
		return
	}
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	h.setSessionCookie(w, r, token)
	jsonResponse(w, http.StatusOK, authResponse{Token: token, User: user})
}

// Logout revokes the caller's server-side session and clears the cookie.
// It needs no valid session: an expired or already revoked token still gets
// its cookie cleared.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if claims := h.sessionClaimsFromRequest(r); claims != nil && claims.SID != "" {
		if err := session.Revoke(r.Context(), h.store, claims.UserID, claims.SID); err != nil && !errors.Is(err, store.ErrSessionNotFound) {
			slog.Error("logout: revoke session", "user_id", claims.UserID, "error", err)
		}
	}
	clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// sessionClaimsFromRequest returns the validated session JWT claims from the
// session cookie or a Bearer header, or nil.
func (h *AuthHandler) sessionClaimsFromRequest(r *http.Request) *auth.Claims {
	var token string
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		token = c.Value
	} else if hdr := r.Header.Get("Authorization"); strings.HasPrefix(hdr, "Bearer ") {
		token = strings.TrimPrefix(hdr, "Bearer ")
	}
	if token == "" {
		return nil
	}
	claims, err := h.jwt.Validate(token)
	if err != nil {
		return nil
	}
	return claims
}
