package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Verification and send limits for personal contact methods.
const (
	contactCodeTTL             = 15 * time.Minute
	contactSendCooldown        = 30 * time.Second
	contactMaxCodeAttempt      = 5
	contactDailySendsPerUser   = 20
	contactDailySMSCodesPerOrg = 50
	// contactDailyCodesPerPhone caps codes to one number per day across
	// every org, matching phoneverify.PhoneDailyLimit, so one number cannot
	// be flooded from many accounts.
	contactDailyCodesPerPhone = 10
)

// CodeMailer sends a plain-text email. *mailer.Mailer satisfies it.
type CodeMailer interface {
	Send(to, subject, body string) error
}

// ContactMethodHandler serves a user's own contact methods and notification
// rules under /users/me, plus a read-only view of a member's methods for
// org admins.
type ContactMethodHandler struct {
	store  store.Store
	sender TestSender
	mailer CodeMailer
	cipher contactmethod.Cipher
	now    func() time.Time
	// telegramLinking is true when the platform bot can receive link codes,
	// so the form offers Connect Telegram.
	telegramLinking bool
	// slackAppDMs and discordAppDMs are true when this server can deliver
	// DMs through the platform Slack and Discord apps (SaaS only).
	slackAppDMs, discordAppDMs bool
}

// NewContactMethodHandler builds the handler. sender delivers test pages
// and codes through the notify providers; mailer (optional) sends email
// codes and test emails through the platform mailer; enc (optional) seals
// provider config the same way the escalation engine does.
func NewContactMethodHandler(s store.Store, sender TestSender, mailer CodeMailer, cipher contactmethod.Cipher) *ContactMethodHandler {
	return &ContactMethodHandler{store: s, sender: sender, mailer: mailer, cipher: cipher, now: time.Now}
}

// SetTelegramLinking reports whether the platform Telegram bot can link
// contact methods with a one-time code.
func (h *ContactMethodHandler) SetTelegramLinking(enabled bool) { h.telegramLinking = enabled }

// SetAppDMs reports whether personal Slack and Discord DMs can send through
// the platform apps: Slack needs the notification key to open the stored
// bot tokens, Discord needs the platform bot token. The self-hosted build
// ignores both.
func (h *ContactMethodHandler) SetAppDMs(slack, discord bool) {
	h.slackAppDMs, h.discordAppDMs = slack, discord
}

// Mount registers the routes on an authenticated router.
func (h *ContactMethodHandler) Mount(r chi.Router) {
	r.Get("/users/me/contact-method-options", h.Options)
	r.Get("/users/me/contact-methods", h.ListMethods)
	r.Post("/users/me/contact-methods", h.CreateMethod)
	r.Get("/users/me/contact-methods/{id}", h.GetMethod)
	r.Patch("/users/me/contact-methods/{id}", h.UpdateMethod)
	r.Delete("/users/me/contact-methods/{id}", h.DeleteMethod)
	r.Post("/users/me/contact-methods/{id}/test", h.TestMethod)
	r.Post("/users/me/contact-methods/{id}/send-code", h.SendCode)
	r.Post("/users/me/contact-methods/{id}/verify", h.Verify)

	r.Get("/users/me/notification-rules", h.ListRules)
	r.Post("/users/me/notification-rules", h.CreateRule)
	r.Patch("/users/me/notification-rules/{id}", h.UpdateRule)
	r.Delete("/users/me/notification-rules/{id}", h.DeleteRule)

	r.Get("/users/paging-readiness", h.PagingReadiness)
	r.With(middleware.RequireRole("owner", "admin")).Get("/users/{id}/contact-methods", h.AdminListForUser)
}

type contactMethodResponse struct {
	ID                  string                   `json:"id"`
	Type                domain.ContactMethodType `json:"type"`
	Label               string                   `json:"label"`
	Config              json.RawMessage          `json:"config"`
	Summary             string                   `json:"summary"`
	Verified            bool                     `json:"verified"`
	VerifiedAt          *time.Time               `json:"verified_at,omitempty"`
	VerificationPending bool                     `json:"verification_pending"`
	PaidOnly            bool                     `json:"paid_only"`
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
}

func (h *ContactMethodHandler) toResponse(m *domain.ContactMethod) contactMethodResponse {
	pending := m.VerifyCodeHash != "" && m.VerifyExpiresAt != nil && h.now().Before(*m.VerifyExpiresAt)
	return contactMethodResponse{
		ID: m.ID, Type: m.Type, Label: m.Label,
		Config:              contactmethod.Redacted(m),
		Summary:             contactmethod.Summary(m),
		Verified:            m.Verified(),
		VerifiedAt:          m.VerifiedAt,
		VerificationPending: pending && !m.Verified(),
		PaidOnly:            contactmethod.PaidOnly(m.Type),
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
	}
}

// adminMethodView is what an org admin sees of a member's method: enough
// to judge whether the person can be paged, with the address masked.
type adminMethodView struct {
	ID       string                   `json:"id"`
	Type     domain.ContactMethodType `json:"type"`
	Label    string                   `json:"label"`
	Summary  string                   `json:"summary"`
	Verified bool                     `json:"verified"`
}

// personal resolves the caller and the contact method stores. It rejects
// API keys: contact methods belong to a signed-in person.
func (h *ContactMethodHandler) personal(w http.ResponseWriter, r *http.Request) (*auth.Claims, store.ContactMethodStore, store.NotificationRuleStore, bool) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.UserID == "" || claims.Role == "api_key" {
		errorResponse(w, http.StatusForbidden, "contact methods can only be managed by a signed-in user")
		return nil, nil, nil, false
	}
	cp, ok := h.store.(store.ContactMethodProvider)
	if !ok {
		errorResponse(w, http.StatusNotImplemented, "contact methods are not supported by this store")
		return nil, nil, nil, false
	}
	return claims, cp.ContactMethods(), cp.NotificationRules(), true
}

// planDenied writes a 403 the SPA recognizes as a plan gate (it shows the
// upgrade prompt for upgrade_required).
func planDenied(w http.ResponseWriter, err error) {
	jsonResponse(w, http.StatusForbidden, map[string]any{"error": err.Error(), "upgrade_required": true})
}

func (h *ContactMethodHandler) ListMethods(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	list, err := methods.ListByUser(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list contact methods")
		return
	}
	out := make([]contactMethodResponse, 0, len(list))
	for _, m := range list {
		out = append(out, h.toResponse(m))
	}
	jsonResponse(w, http.StatusOK, out)
}

func (h *ContactMethodHandler) GetMethod(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	m, err := methods.Get(r.Context(), claims.UserID, chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusNotFound, "contact method not found")
		return
	}
	jsonResponse(w, http.StatusOK, h.toResponse(m))
}

func cleanLabel(label string, t domain.ContactMethodType) (string, error) {
	label = strings.TrimSpace(label)
	if strings.ContainsAny(label, "\r\n") || utf8.RuneCountInString(label) > contactmethod.MaxLabelLength {
		return "", fmt.Errorf("label must be one line of at most %d characters", contactmethod.MaxLabelLength)
	}
	if label == "" {
		label = defaultLabel(t)
	}
	return label, nil
}

func defaultLabel(t domain.ContactMethodType) string {
	switch t {
	case domain.ContactEmail:
		return "Email"
	case domain.ContactSMS:
		return "SMS"
	case domain.ContactVoice:
		return "Phone call"
	case domain.ContactNtfy:
		return "ntfy push"
	case domain.ContactPushover:
		return "Pushover"
	case domain.ContactPagerDuty:
		return "PagerDuty"
	case domain.ContactWebhook:
		return "Webhook"
	case domain.ContactSlackDM:
		return "Slack DM"
	case domain.ContactDiscordDM:
		return "Discord DM"
	case domain.ContactTelegram:
		return "Telegram"
	}
	return string(t)
}

func (h *ContactMethodHandler) CreateMethod(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	var req struct {
		Type   domain.ContactMethodType `json:"type"`
		Label  string                   `json:"label"`
		Config json.RawMessage          `json:"config"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !contactmethod.KnownType(req.Type) {
		errorResponse(w, http.StatusBadRequest, "unknown contact method type")
		return
	}
	if err := contactmethod.PlanError(r.Context(), h.store, claims.OrgID, req.Type); err != nil {
		planDenied(w, err)
		return
	}
	label, err := cleanLabel(req.Label, req.Type)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := contactmethod.Normalize(r.Context(), h.store, claims.OrgID, req.Type, req.Config, "")
	if err != nil {
		if contactmethod.IsValidation(err) {
			errorResponse(w, http.StatusBadRequest, err.Error())
		} else {
			errorResponse(w, http.StatusInternalServerError, "failed to validate contact method")
		}
		return
	}
	cfg, err = contactmethod.SealConfig(req.Type, cfg, h.cipher.Enc)
	if err != nil {
		sealFailed(w, err)
		return
	}
	n, err := methods.CountByUser(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create contact method")
		return
	}
	if n >= contactmethod.MaxMethodsPerUser {
		errorResponse(w, http.StatusConflict, fmt.Sprintf("you can keep at most %d contact methods", contactmethod.MaxMethodsPerUser))
		return
	}

	now := h.now().UTC()
	m := &domain.ContactMethod{
		ID: uuid.New().String(), OrgID: claims.OrgID, UserID: claims.UserID,
		Type: req.Type, Label: label, Config: cfg, CreatedAt: now, UpdatedAt: now,
	}
	// The verified account email needs no second confirmation.
	if req.Type == domain.ContactEmail {
		if u, err := h.store.Users().GetByID(r.Context(), claims.UserID); err == nil && u.EmailVerifiedAt != nil &&
			strings.EqualFold(emailOf(cfg), u.Email) {
			m.VerifiedAt = &now
		}
	}
	if err := methods.Create(r.Context(), m); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create contact method")
		return
	}
	jsonResponse(w, http.StatusCreated, h.toResponse(m))
}

// sealFailed reports a SealConfig error. A missing encryption key on the
// hosted build is a server configuration problem, so it is a 503 with the
// reason; anything else is a 500.
func sealFailed(w http.ResponseWriter, err error) {
	if errors.Is(err, contactmethod.ErrSealingRequired) {
		errorResponse(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	errorResponse(w, http.StatusInternalServerError, "failed to store contact method")
}

func emailOf(cfg string) string {
	var c struct {
		Address string `json:"address"`
	}
	_ = json.Unmarshal([]byte(cfg), &c)
	return c.Address
}

func (h *ContactMethodHandler) UpdateMethod(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	m, err := methods.Get(r.Context(), claims.UserID, chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusNotFound, "contact method not found")
		return
	}
	var req struct {
		Label  *string          `json:"label"`
		Config *json.RawMessage `json:"config"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Label != nil {
		label, err := cleanLabel(*req.Label, m.Type)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		m.Label = label
	}
	opened, err := contactmethod.OpenConfig(m.Type, m.Config, h.cipher.Dec)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read contact method")
		return
	}
	reset := false
	if req.Config != nil {
		cfg, err := contactmethod.Normalize(r.Context(), h.store, claims.OrgID, m.Type, *req.Config, opened)
		if err != nil {
			if contactmethod.IsValidation(err) {
				errorResponse(w, http.StatusBadRequest, err.Error())
			} else {
				errorResponse(w, http.StatusInternalServerError, "failed to validate contact method")
			}
			return
		}
		// A new destination must be confirmed again before it is paged.
		if cfg != opened {
			reset = true
			if m.Config, err = contactmethod.SealConfig(m.Type, cfg, h.cipher.Enc); err != nil {
				sealFailed(w, err)
				return
			}
		}
	}
	if err := methods.Update(r.Context(), m, reset); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update contact method")
		return
	}
	if reset {
		m.VerifyCodeHash, m.VerifyExpiresAt = "", nil
	}
	jsonResponse(w, http.StatusOK, h.toResponse(m))
}

func (h *ContactMethodHandler) DeleteMethod(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	if err := methods.Delete(r.Context(), claims.UserID, chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, store.ErrContactMethodNotFound) {
			errorResponse(w, http.StatusNotFound, "contact method not found")
		} else {
			errorResponse(w, http.StatusInternalServerError, "failed to delete contact method")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cooling reports whether the method was sent something too recently and,
// if so, writes a 429 with Retry-After.
func (h *ContactMethodHandler) cooling(w http.ResponseWriter, m *domain.ContactMethod) bool {
	if m.LastSentAt == nil {
		return false
	}
	wait := m.LastSentAt.Add(contactSendCooldown).Sub(h.now())
	if wait <= 0 {
		return false
	}
	secs := int(wait.Seconds()) + 1
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	errorResponse(w, http.StatusTooManyRequests, fmt.Sprintf("please wait %d seconds before sending again", secs))
	return true
}

// deliver sends subject/message to m. Email goes through the platform
// mailer when one is configured; everything else, and email without a
// platform mailer, goes through the notify provider for the method.
func (h *ContactMethodHandler) deliver(r *http.Request, m *domain.ContactMethod, subject, message string) error {
	if m.Type == domain.ContactEmail && h.mailer != nil {
		return h.mailer.Send(emailOf(m.Config), subject, message)
	}
	if h.sender == nil {
		return errNoDelivery
	}
	job := domain.NotificationJob{
		ID:          uuid.New().String(),
		AlertID:     "test-" + uuid.New().String(),
		OrgID:       m.OrgID,
		MonitorName: subject,
		Severity:    "info",
		Message:     message,
		DedupeKey:   "contact-" + uuid.New().String(),
	}
	target := m
	if m.Type == domain.ContactVoice {
		// A voice call cannot read a code back reliably; text the number.
		sms := *m
		sms.Type = domain.ContactSMS
		target = &sms
	}
	if err := contactmethod.FillJob(r.Context(), h.store, target, &job, h.cipher); err != nil {
		return err
	}
	job.ContactMethodID = m.ID
	_, err := h.sender.TestSend(r.Context(), job)
	return err
}

var errNoDelivery = errors.New("notification delivery is not configured on this server")

// reserveSend takes the daily send budget. phone is the destination number
// for an SMS or voice code, empty for anything else.
func (h *ContactMethodHandler) reserveSend(w http.ResponseWriter, r *http.Request, claims *auth.Claims, methods store.ContactMethodStore, phone string) bool {
	ok, err := methods.ReserveSendBudget(r.Context(), claims.UserID, claims.OrgID, h.now().UTC(), phone,
		contactDailySendsPerUser, contactDailySMSCodesPerOrg, contactDailyCodesPerPhone)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to check the send budget")
		return false
	}
	if ok {
		return true
	}
	now := h.now().UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	w.Header().Set("Retry-After", strconv.Itoa(int(next.Sub(now).Seconds())+1))
	errorResponse(w, http.StatusTooManyRequests, "daily send limit reached; try again tomorrow")
	return false
}

func (h *ContactMethodHandler) TestMethod(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	m, err := methods.Get(r.Context(), claims.UserID, chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusNotFound, "contact method not found")
		return
	}
	if !m.Verified() {
		errorResponse(w, http.StatusConflict, "verify this contact method before sending a test")
		return
	}
	if err := contactmethod.PlanError(r.Context(), h.store, claims.OrgID, m.Type); err != nil {
		planDenied(w, err)
		return
	}
	if h.cooling(w, m) {
		return
	}
	if !h.reserveSend(w, r, claims, methods, "") {
		return
	}
	if err := methods.RecordSend(r.Context(), claims.UserID, m.ID, h.now().UTC()); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to send test")
		return
	}
	msg := fmt.Sprintf("This is a test page from YipYap to your %q contact method. No action is needed.", m.Label)
	if err := h.deliver(r, m, "YipYap test page", msg); err != nil {
		slog.Warn("contact method test send failed", "method_id", m.ID, "type", m.Type, "error", err)
		h.deliveryFailed(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "test sent"})
}

func (h *ContactMethodHandler) deliveryFailed(w http.ResponseWriter, err error) {
	if errors.Is(err, errNoDelivery) {
		errorResponse(w, http.StatusServiceUnavailable, errNoDelivery.Error())
		return
	}
	// A provider that knows why the destination refused (Discord will not
	// DM this user, the Slack member does not exist) says so.
	var re *notify.RecipientError
	if errors.As(err, &re) {
		errorResponse(w, http.StatusBadGateway, notify.PublicDeliveryError(err))
		return
	}
	errorResponse(w, http.StatusBadGateway, "could not deliver to this contact method. Check the details and try again")
}

func (h *ContactMethodHandler) SendCode(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	m, err := methods.Get(r.Context(), claims.UserID, chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusNotFound, "contact method not found")
		return
	}
	if m.Verified() {
		errorResponse(w, http.StatusConflict, "this contact method is already verified")
		return
	}
	if err := contactmethod.PlanError(r.Context(), h.store, claims.OrgID, m.Type); err != nil {
		planDenied(w, err)
		return
	}
	if h.cooling(w, m) {
		return
	}
	phone := ""
	if m.Type == domain.ContactSMS || m.Type == domain.ContactVoice {
		if phone = contactmethod.PhoneOf(m); phone == "" {
			errorResponse(w, http.StatusInternalServerError, "failed to read contact method")
			return
		}
	}
	if !h.reserveSend(w, r, claims, methods, phone) {
		return
	}
	code, hash, err := contactmethod.NewCode(m.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create a verification code")
		return
	}
	now := h.now().UTC()
	if err := methods.SetVerificationCode(r.Context(), claims.UserID, m.ID, hash, now.Add(contactCodeTTL), now); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create a verification code")
		return
	}
	msg := fmt.Sprintf("Your YipYap verification code is %s. It expires in %d minutes.", code, int(contactCodeTTL.Minutes()))
	if err := h.deliver(r, m, "YipYap verification code", msg); err != nil {
		slog.Warn("contact method code send failed", "method_id", m.ID, "type", m.Type, "error", err)
		h.deliveryFailed(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"status":     "code sent",
		"expires_at": now.Add(contactCodeTTL),
	})
}

func (h *ContactMethodHandler) Verify(w http.ResponseWriter, r *http.Request) {
	claims, methods, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	m, err := methods.Get(r.Context(), claims.UserID, chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusNotFound, "contact method not found")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if m.Verified() {
		jsonResponse(w, http.StatusOK, h.toResponse(m))
		return
	}
	code := strings.TrimSpace(req.Code)
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		errorResponse(w, http.StatusBadRequest, "enter the 6-digit code")
		return
	}
	if m.VerifyCodeHash == "" || m.VerifyExpiresAt == nil || !h.now().Before(*m.VerifyExpiresAt) {
		errorResponse(w, http.StatusBadRequest, "this code has expired. Send a new one")
		return
	}
	// Count the attempt before checking so parallel guesses cannot exceed
	// the limit.
	attempts, err := methods.IncrementVerifyAttempts(r.Context(), claims.UserID, m.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to verify code")
		return
	}
	if attempts > contactMaxCodeAttempt {
		errorResponse(w, http.StatusTooManyRequests, "too many attempts. Send a new code")
		return
	}
	if !contactmethod.CodeMatches(m.ID, code, m.VerifyCodeHash) {
		errorResponse(w, http.StatusBadRequest, "that code is not correct")
		return
	}
	now := h.now().UTC()
	if err := methods.MarkVerified(r.Context(), claims.UserID, m.ID, now); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to verify code")
		return
	}
	m.VerifiedAt, m.VerifyCodeHash, m.VerifyExpiresAt = &now, "", nil
	jsonResponse(w, http.StatusOK, h.toResponse(m))
}

type ruleRequest struct {
	ContactMethodID *string                     `json:"contact_method_id"`
	DelayMinutes    *int                        `json:"delay_minutes"`
	Urgency         *domain.NotificationUrgency `json:"urgency"`
}

func validateRule(rule *domain.NotificationRule) error {
	if rule.DelayMinutes < 0 || rule.DelayMinutes > contactmethod.MaxDelayMinutes {
		return fmt.Errorf("delay_minutes must be between 0 and %d", contactmethod.MaxDelayMinutes)
	}
	switch rule.Urgency {
	case domain.UrgencyHigh, domain.UrgencyLow, domain.UrgencyAny:
	default:
		return fmt.Errorf("urgency must be high, low or any")
	}
	if rule.ContactMethodID == "" {
		return fmt.Errorf("contact_method_id is required")
	}
	return nil
}

// duplicateRule writes a 409 when the caller already has a rule with the
// same method, delay and urgency, which would only send the page twice.
func (h *ContactMethodHandler) duplicateRule(w http.ResponseWriter, r *http.Request, rules store.NotificationRuleStore, rule *domain.NotificationRule) bool {
	existing, err := rules.ListByUser(r.Context(), rule.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to check notification rules")
		return true
	}
	for _, e := range existing {
		if e.ID != rule.ID && e.ContactMethodID == rule.ContactMethodID &&
			e.DelayMinutes == rule.DelayMinutes && e.Urgency == rule.Urgency {
			errorResponse(w, http.StatusConflict, "you already have a rule for this contact method with the same delay and urgency")
			return true
		}
	}
	return false
}

func (h *ContactMethodHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	claims, _, rules, ok := h.personal(w, r)
	if !ok {
		return
	}
	list, err := rules.ListByUser(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list notification rules")
		return
	}
	jsonResponse(w, http.StatusOK, list)
}

func (h *ContactMethodHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	claims, _, rules, ok := h.personal(w, r)
	if !ok {
		return
	}
	var req ruleRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule := &domain.NotificationRule{
		ID: uuid.New().String(), OrgID: claims.OrgID, UserID: claims.UserID,
		Urgency: domain.UrgencyAny, CreatedAt: h.now().UTC(),
	}
	if req.ContactMethodID != nil {
		rule.ContactMethodID = *req.ContactMethodID
	}
	if req.DelayMinutes != nil {
		rule.DelayMinutes = *req.DelayMinutes
	}
	if req.Urgency != nil {
		rule.Urgency = *req.Urgency
	}
	if err := validateRule(rule); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := rules.CountByUser(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create notification rule")
		return
	}
	if h.duplicateRule(w, r, rules, rule) {
		return
	}
	if n >= contactmethod.MaxRulesPerUser {
		errorResponse(w, http.StatusConflict, fmt.Sprintf("you can keep at most %d notification rules", contactmethod.MaxRulesPerUser))
		return
	}
	if err := rules.Create(r.Context(), rule); err != nil {
		if errors.Is(err, store.ErrContactMethodNotFound) {
			errorResponse(w, http.StatusBadRequest, "contact method not found")
		} else {
			errorResponse(w, http.StatusInternalServerError, "failed to create notification rule")
		}
		return
	}
	jsonResponse(w, http.StatusCreated, rule)
}

func (h *ContactMethodHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	claims, methods, rules, ok := h.personal(w, r)
	if !ok {
		return
	}
	rule, err := rules.Get(r.Context(), claims.UserID, chi.URLParam(r, "id"))
	if err != nil {
		errorResponse(w, http.StatusNotFound, "notification rule not found")
		return
	}
	var req ruleRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ContactMethodID != nil {
		rule.ContactMethodID = *req.ContactMethodID
	}
	if req.DelayMinutes != nil {
		rule.DelayMinutes = *req.DelayMinutes
	}
	if req.Urgency != nil {
		rule.Urgency = *req.Urgency
	}
	if err := validateRule(rule); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := methods.Get(r.Context(), claims.UserID, rule.ContactMethodID); err != nil {
		errorResponse(w, http.StatusBadRequest, "contact method not found")
		return
	}
	if h.duplicateRule(w, r, rules, rule) {
		return
	}
	if err := rules.Update(r.Context(), rule); err != nil {
		if errors.Is(err, store.ErrNotificationRuleNotFound) {
			errorResponse(w, http.StatusNotFound, "notification rule not found")
		} else {
			errorResponse(w, http.StatusInternalServerError, "failed to update notification rule")
		}
		return
	}
	jsonResponse(w, http.StatusOK, rule)
}

func (h *ContactMethodHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	claims, _, rules, ok := h.personal(w, r)
	if !ok {
		return
	}
	if err := rules.Delete(r.Context(), claims.UserID, chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, store.ErrNotificationRuleNotFound) {
			errorResponse(w, http.StatusNotFound, "notification rule not found")
		} else {
			errorResponse(w, http.StatusInternalServerError, "failed to delete notification rule")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AdminListForUser lets an org owner or admin see how a member will be
// paged (for the on-call view). It is read-only and masks addresses.
func (h *ContactMethodHandler) AdminListForUser(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.Role == "api_key" {
		errorResponse(w, http.StatusForbidden, "forbidden")
		return
	}
	cp, ok := h.store.(store.ContactMethodProvider)
	if !ok {
		errorResponse(w, http.StatusNotImplemented, "contact methods are not supported by this store")
		return
	}
	userID := chi.URLParam(r, "id")
	if userID == "me" {
		userID = claims.UserID
	}
	u, err := h.store.Users().GetByID(r.Context(), userID)
	if err != nil || u.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	list, err := cp.ContactMethods().ListByUser(r.Context(), u.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list contact methods")
		return
	}
	rules, err := cp.NotificationRules().ListByUser(r.Context(), u.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list notification rules")
		return
	}
	views := make([]adminMethodView, 0, len(list))
	for _, m := range list {
		views = append(views, adminMethodView{
			ID: m.ID, Type: m.Type, Label: m.Label,
			Summary: contactmethod.Summary(m), Verified: m.Verified(),
		})
	}
	if rules == nil {
		rules = []*domain.NotificationRule{}
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"user_id":            u.ID,
		"contact_methods":    views,
		"notification_rules": rules,
	})
}
