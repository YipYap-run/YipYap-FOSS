package domain

import "time"

// OIDCDomain is an email domain that routes "Sign in with SSO" to one OIDC
// connection. It routes only after the org proves ownership by publishing
// VerificationToken in a TXT record at _yipyap-verify.<Domain>.
type OIDCDomain struct {
	ID                string     `json:"id"`
	OrgID             string     `json:"org_id"`
	ConnectionID      string     `json:"connection_id"`
	Domain            string     `json:"domain"`
	VerificationToken string     `json:"verification_token"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// Verified reports whether the domain has passed DNS verification.
func (d *OIDCDomain) Verified() bool { return d.VerifiedAt != nil }
