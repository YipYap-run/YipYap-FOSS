package domain

import "time"

type WebAuthnCredential struct {
	ID              string    `json:"id"`
	UserID          string    `json:"user_id"`
	OrgID           string    `json:"org_id"`
	Name            string    `json:"name"`
	PublicKey       []byte    `json:"-"`
	AttestationType string    `json:"-"`
	SignCount       uint32    `json:"-"`
	Discoverable    bool      `json:"discoverable"`
	UserHandle      []byte    `json:"-"`
	Transports      []string  `json:"-"`
	// BackupEligible and BackupState are the BE/BS flags from the
	// authenticator data at registration. The library rejects assertions
	// whose BackupEligible differs from the stored credential; platform
	// authenticators emit BE=true and must be stored that way to verify
	// at login. Defaults (both false) are correct for legacy security keys.
	BackupEligible bool      `json:"-"`
	BackupState    bool      `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}
