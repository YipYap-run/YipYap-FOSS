package sqlx

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// MFAStore is a no-op stub in FOSS builds.
type MFAStore struct{}

// NewMFAStore returns a no-op MFAStore for FOSS builds.
func NewMFAStore(_ Queryable, _ Dialect) *MFAStore { return &MFAStore{} }

func (s *MFAStore) SetTOTPSecret(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) GetTOTPSecret(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) EnableTOTP(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) DisableTOTP(_ context.Context, _ string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) GetBackupCodes(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *MFAStore) UseBackupCode(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) CreateWebAuthnCredential(_ context.Context, _ *domain.WebAuthnCredential) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) ListWebAuthnCredentials(_ context.Context, _ string) ([]*domain.WebAuthnCredential, error) {
	return nil, nil
}

func (s *MFAStore) GetWebAuthnCredential(_ context.Context, _ string) (*domain.WebAuthnCredential, error) {
	return nil, fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) GetWebAuthnCredentialByUserHandle(_ context.Context, _ []byte) (*domain.WebAuthnCredential, error) {
	return nil, fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) UpdateWebAuthnSignCount(_ context.Context, _ string, _ uint32) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) DeleteWebAuthnCredential(_ context.Context, _ string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}
