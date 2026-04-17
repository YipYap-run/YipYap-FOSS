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
	CreatedAt       time.Time `json:"created_at"`
}
