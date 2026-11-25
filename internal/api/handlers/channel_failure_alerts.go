package handlers

import (
	"net/http"
	"strconv"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// ChannelFailureAlertsHandler reads and writes how many failed deliveries
// in a row make the notifier email the org owners about a channel.
type ChannelFailureAlertsHandler struct {
	store store.Store
	// emailEnabled is false when the server has no platform mailer, so
	// the SPA can say the emails are not sent.
	emailEnabled bool
}

// NewChannelFailureAlertsHandler returns a handler bound to s.
func NewChannelFailureAlertsHandler(s store.Store) *ChannelFailureAlertsHandler {
	return &ChannelFailureAlertsHandler{store: s, emailEnabled: true}
}

// SetEmailEnabled records whether the server can send the owner emails.
func (h *ChannelFailureAlertsHandler) SetEmailEnabled(v bool) { h.emailEnabled = v }

type channelFailureAlertsResponse struct {
	Threshold    int  `json:"threshold"`
	Default      int  `json:"default"`
	Min          int  `json:"min"`
	Max          int  `json:"max"`
	CanConfigure bool `json:"can_configure"`
	EmailEnabled bool `json:"email_enabled"`
}

func (h *ChannelFailureAlertsHandler) respond(w http.ResponseWriter, r *http.Request, threshold int) {
	role := middleware.GetClaims(r.Context()).Role
	jsonResponse(w, http.StatusOK, channelFailureAlertsResponse{
		Threshold:    threshold,
		Default:      domain.DefaultChannelFailureAlertThreshold,
		Min:          domain.MinChannelFailureAlertThreshold,
		Max:          domain.MaxChannelFailureAlertThreshold,
		CanConfigure: role == string(domain.RoleOwner) || role == string(domain.RoleAdmin),
		EmailEnabled: h.emailEnabled,
	})
}

// Get returns the org's threshold. Any member of the org may read it.
func (h *ChannelFailureAlertsHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	sp, ok := h.store.(store.OrgSettingsProvider)
	if !ok {
		h.respond(w, r, domain.DefaultChannelFailureAlertThreshold)
		return
	}
	all, err := sp.OrgSettings().GetAll(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	h.respond(w, r, domain.ChannelFailureAlertThresholdFromSettings(all))
}

type channelFailureAlertsRequest struct {
	Threshold *int `json:"threshold"`
}

// Update sets the org's threshold. Owners and admins only (enforced by
// the router).
func (h *ChannelFailureAlertsHandler) Update(w http.ResponseWriter, r *http.Request) {
	sp, ok := h.store.(store.OrgSettingsProvider)
	if !ok {
		errorResponse(w, http.StatusNotImplemented, "settings not available")
		return
	}
	var body channelFailureAlertsRequest
	if err := decodeBody(r, &body); err != nil {
		errorResponse(w, http.StatusBadRequest, "threshold must be a whole number")
		return
	}
	if body.Threshold == nil {
		errorResponse(w, http.StatusBadRequest, "threshold is required")
		return
	}
	if err := domain.ValidateChannelFailureAlertThreshold(*body.Threshold); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	claims := middleware.GetClaims(r.Context())
	if err := sp.OrgSettings().Set(r.Context(), claims.OrgID, domain.SettingChannelFailureAlertThreshold, strconv.Itoa(*body.Threshold)); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to save settings")
		return
	}
	h.respond(w, r, *body.Threshold)
}
