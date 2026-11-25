package handlers

import (
	"net/http"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// NotificationPermissionsHandler reads and writes which roles may manage
// and test notification channels in the caller's org.
type NotificationPermissionsHandler struct {
	store store.Store
}

// NewNotificationPermissionsHandler returns a handler bound to s.
func NewNotificationPermissionsHandler(s store.Store) *NotificationPermissionsHandler {
	return &NotificationPermissionsHandler{store: s}
}

// notificationPermissionsResponse is the org setting plus what the caller
// may do with it. The SPA gates its controls on the can_* fields rather
// than reimplementing the role rules.
type notificationPermissionsResponse struct {
	ManageRoles  []string                  `json:"manage_roles"`
	TestRoles    []string                  `json:"test_roles"`
	Defaults     domain.ChannelPermissions `json:"defaults"`
	CanManage    bool                      `json:"can_manage"`
	CanTest      bool                      `json:"can_test"`
	CanConfigure bool                      `json:"can_configure"`
}

func (h *NotificationPermissionsHandler) respond(w http.ResponseWriter, r *http.Request, p domain.ChannelPermissions) {
	claims := middleware.GetClaims(r.Context())
	role := claims.Role
	jsonResponse(w, http.StatusOK, notificationPermissionsResponse{
		ManageRoles:  p.ManageRoles,
		TestRoles:    p.TestRoles,
		Defaults:     domain.DefaultChannelPermissions(),
		CanManage:    middleware.ChannelPermissionAllows(p, role, domain.ChannelActionManage),
		CanTest:      middleware.ChannelPermissionAllows(p, role, domain.ChannelActionTest),
		CanConfigure: role == string(domain.RoleOwner) || role == string(domain.RoleAdmin),
	})
}

// Get returns the org's channel permissions and the caller's effective
// rights. Any authenticated member of the org may read it.
func (h *NotificationPermissionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	p, err := middleware.LoadChannelPermissions(r.Context(), h.store, claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load permissions")
		return
	}
	h.respond(w, r, p)
}

type notificationPermissionsRequest struct {
	ManageRoles *[]string `json:"manage_roles"`
	TestRoles   *[]string `json:"test_roles"`
}

// Update replaces the org's channel permissions. Owners and admins only
// (enforced by the router). A field left out keeps its current value. The
// owner is always added to both lists and viewers can never manage.
func (h *NotificationPermissionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	sp, ok := h.store.(store.OrgSettingsProvider)
	if !ok {
		errorResponse(w, http.StatusNotImplemented, "settings not available")
		return
	}
	claims := middleware.GetClaims(r.Context())
	var body notificationPermissionsRequest
	if err := decodeBody(r, &body); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.ManageRoles == nil && body.TestRoles == nil {
		errorResponse(w, http.StatusBadRequest, "manage_roles or test_roles is required")
		return
	}
	cur, err := middleware.LoadChannelPermissions(r.Context(), h.store, claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load permissions")
		return
	}
	next := cur
	if body.ManageRoles != nil {
		next.ManageRoles = *body.ManageRoles
	}
	if body.TestRoles != nil {
		next.TestRoles = *body.TestRoles
	}
	norm, err := domain.NormalizeChannelPermissions(next)
	if err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	settings := sp.OrgSettings()
	if err := settings.Set(r.Context(), claims.OrgID, domain.SettingChannelManageRoles, strings.Join(norm.ManageRoles, ",")); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to save permissions")
		return
	}
	if err := settings.Set(r.Context(), claims.OrgID, domain.SettingChannelTestRoles, strings.Join(norm.TestRoles, ",")); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to save permissions")
		return
	}
	h.respond(w, r, norm)
}
