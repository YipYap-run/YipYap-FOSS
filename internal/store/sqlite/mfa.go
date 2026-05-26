package sqlite

import (
	"context"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

type mfaStore struct{ q queryable }

func (s *mfaStore) SetTOTPSecret(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) GetTOTPSecret(_ context.Context, _ string) (string, error) {
	return "", fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) EnableTOTP(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) DisableTOTP(_ context.Context, _ string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) GetBackupCodes(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (s *mfaStore) UseBackupCode(_ context.Context, _ string, _ []string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) CreateWebAuthnCredential(_ context.Context, _ *domain.WebAuthnCredential) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) ListWebAuthnCredentials(_ context.Context, _ string) ([]*domain.WebAuthnCredential, error) {
	return nil, nil
}

func (s *mfaStore) GetWebAuthnCredential(_ context.Context, _ string) (*domain.WebAuthnCredential, error) {
	return nil, fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) GetWebAuthnCredentialByUserHandle(_ context.Context, _ []byte) (*domain.WebAuthnCredential, error) {
	return nil, fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) UpdateWebAuthnSignCount(_ context.Context, _ string, _ uint32) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *mfaStore) DeleteWebAuthnCredential(_ context.Context, _ string) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}
