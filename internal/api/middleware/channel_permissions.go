package middleware

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// LoadChannelPermissions returns the org's effective notification channel
// permissions. Stores without org settings get the defaults.
func LoadChannelPermissions(ctx context.Context, s store.Store, orgID string) (domain.ChannelPermissions, error) {
	sp, ok := s.(store.OrgSettingsProvider)
	if !ok {
		return domain.DefaultChannelPermissions(), nil
	}
	all, err := sp.OrgSettings().GetAll(ctx, orgID)
	if err != nil {
		return domain.ChannelPermissions{}, err
	}
	return domain.ChannelPermissionsFromSettings(all), nil
}

// ChannelPermissionAllows reports whether the caller may perform action.
// API keys are not subject to the org's role setting; RequireScope and
// RequireWriteAccess gate them instead.
func ChannelPermissionAllows(p domain.ChannelPermissions, role string, action domain.ChannelAction) bool {
	if role == "api_key" {
		return true
	}
	return p.Allows(role, action)
}

// RequireChannelPermission rejects callers whose role the org has not
// allowed to perform action on notification channels.
func RequireChannelPermission(s store.Store, action domain.ChannelAction) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := GetClaims(r.Context())
			if claims == nil {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}
			if claims.Role == "api_key" {
				next.ServeHTTP(w, r)
				return
			}
			p, err := LoadChannelPermissions(r.Context(), s, claims.OrgID)
			if err != nil {
				slog.Error("load channel permissions", "org_id", claims.OrgID, "error", err)
				writeError(w, http.StatusInternalServerError, "failed to load permissions")
				return
			}
			if !p.Allows(claims.Role, action) {
				msg := "your role cannot change notification channels"
				if action == domain.ChannelActionTest {
					msg = "your role cannot send test notifications"
				}
				writeError(w, http.StatusForbidden, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
