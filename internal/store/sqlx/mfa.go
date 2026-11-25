package sqlx

import (
	"context"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
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

func (s *MFAStore) ConsumeBackupCode(_ context.Context, _ string, _ string) (bool, error) {
	return false, fmt.Errorf("MFA not available in FOSS edition")
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

func (s *MFAStore) AcceptTOTPStep(_ context.Context, _ string, _ int64) (bool, error) {
	return false, fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) MFALockedUntil(_ context.Context, _ string, _ time.Time) (time.Time, error) {
	return time.Time{}, nil
}

func (s *MFAStore) RecordMFAFailure(_ context.Context, _ string, _ time.Time, _ store.MFALockoutPolicy) (time.Time, error) {
	return time.Time{}, nil
}

func (s *MFAStore) ResetMFAFailures(_ context.Context, _ string) error { return nil }

func (s *MFAStore) PutChallenge(_ context.Context, _ string, _ []byte, _ time.Time) error {
	return fmt.Errorf("MFA not available in FOSS edition")
}

func (s *MFAStore) TakeChallenge(_ context.Context, _ string, _ time.Time) ([]byte, error) {
	return nil, store.ErrChallengeNotFound
}

func (s *MFAStore) DeleteExpiredChallenges(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}
