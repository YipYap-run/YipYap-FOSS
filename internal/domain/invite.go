package domain

import "time"

// InviteTTL is how long a member invite link stays valid. Resending an
// invite issues a new link and restarts the window.
const InviteTTL = 7 * 24 * time.Hour

// OrgInvite is an invitation for an email address to join an org with a
// role. The accept link carries a secret; only its SHA-256 hash is stored.
type OrgInvite struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	Email      string     `json:"email"`
	Role       UserRole   `json:"role"`
	TokenHash  string     `json:"-"`
	InvitedBy  string     `json:"invited_by"`
	ExpiresAt  time.Time  `json:"expires_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Pending reports whether the invite has been neither accepted nor revoked.
func (i *OrgInvite) Pending() bool {
	return i.AcceptedAt == nil && i.RevokedAt == nil
}

// Expired reports whether the invite link has expired at now.
func (i *OrgInvite) Expired(now time.Time) bool {
	return !now.Before(i.ExpiresAt)
}
