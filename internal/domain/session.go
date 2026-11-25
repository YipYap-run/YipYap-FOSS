package domain

import "time"

// Session auth methods. These match the JWT amr values. FOSS keeps password
// and SSO only.
const (
	SessionAuthPassword = "password"
	SessionAuthSSO      = "sso"
)

// Session is a server-side record of one signed-in browser or client. Every
// session JWT carries the session ID in its sid claim, and the auth
// middleware rejects the token once the row is revoked, expired or gone.
type Session struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	OrgID      string     `json:"org_id"`
	AuthMethod string     `json:"auth_method"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	UserAgent  string     `json:"user_agent,omitempty"`
	IP         string     `json:"ip,omitempty"`
}

// Active reports whether the session can still authenticate at now.
func (s *Session) Active(now time.Time) bool {
	return s.RevokedAt == nil && now.Before(s.ExpiresAt)
}
