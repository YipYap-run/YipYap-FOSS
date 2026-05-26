package domain

import "time"

type UserRole string

const (
	RoleOwner  UserRole = "owner"
	RoleAdmin  UserRole = "admin"
	RoleMember UserRole = "member"
	RoleViewer UserRole = "viewer"
)

// FreeMaxMembers is the maximum number of users allowed on the free plan.
const FreeMaxMembers = 5

type User struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Email        string    `json:"email"`
	Name         string    `json:"name,omitempty"`
	PasswordHash string    `json:"-"`
	Role         UserRole  `json:"role"`
	Phone               string    `json:"phone,omitempty"`
	ForcePasswordChange bool      `json:"force_password_change"`
	MFAAppEnabled       bool      `json:"mfa_app_enabled"`
	MFAEnforcedAt       string    `json:"mfa_enforced_at,omitempty"`
	// EmailVerifiedAt is nil until the user clicks the verification link
	// emailed at registration. Login is blocked while nil.
	EmailVerifiedAt                      *time.Time `json:"email_verified_at,omitempty"`
	EmailVerificationSentAt              *time.Time `json:"-"`
	EmailVerificationResendCount         int        `json:"-"`
	EmailVerificationResendWindowStarted *time.Time `json:"-"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DisabledAt   *time.Time `json:"disabled_at,omitempty"`
}

type OIDCConnection struct {
	ID            string   `json:"id"`
	OrgID         string   `json:"org_id"`
	Provider      string   `json:"provider"`
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"-"`
	IssuerURL     string   `json:"issuer_url,omitempty"`
	AuthorizeURL  string   `json:"authorize_url,omitempty"`
	TokenURL      string   `json:"token_url,omitempty"`
	UserinfoURL   string   `json:"userinfo_url,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
	AutoProvision bool     `json:"auto_provision"`
	DefaultRole   UserRole `json:"default_role"`
	Enabled       bool     `json:"enabled"`
}

type UserOIDCLink struct {
	UserID            string `json:"user_id"`
	OIDCConnectionID  string `json:"oidc_connection_id"`
	ExternalSubjectID string `json:"external_subject_id"`
}
