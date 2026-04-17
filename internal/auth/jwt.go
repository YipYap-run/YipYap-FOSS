package auth

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims holds the custom JWT claims for yipyap.
type Claims struct {
	UserID string   `json:"user_id"`
	OrgID  string   `json:"org_id"`
	Role   string   `json:"role"`
	Nonce  string   `json:"nonce,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
	jwt.RegisteredClaims
}

// JWTIssuer creates and validates HS256-signed JWTs.
type JWTIssuer struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTIssuer returns a JWTIssuer configured with the given secret and token TTL.
func NewJWTIssuer(secret []byte, ttl time.Duration) *JWTIssuer {
	return &JWTIssuer{secret: secret, ttl: ttl}
}

// TTL returns the token time-to-live configured for this issuer.
func (j *JWTIssuer) TTL() time.Duration {
	return j.ttl
}

// Issue creates a signed JWT string for the given user, org, and role.
func (j *JWTIssuer) Issue(userID, orgID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(j.ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// IssueWithTTL creates a signed JWT with a custom TTL instead of the default.
func (j *JWTIssuer) IssueWithTTL(userID, orgID, role string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// IssueMFA creates a short-lived token (5 min) with aud:"mfa" that can only
// be used at the MFA challenge endpoint.
func (j *JWTIssuer) IssueMFA(userID, orgID, role string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   role,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute)),
			Audience:  jwt.ClaimStrings{"mfa"},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// ValidateMFA parses an MFA token, checking aud:"mfa" and expiry.
func (j *JWTIssuer) ValidateMFA(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return j.secret, nil
	}, jwt.WithAudience("mfa"))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid MFA token")
	}
	return claims, nil
}

// Validate parses and validates the token string, returning the claims on success.
func (j *JWTIssuer) Validate(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	if len(claims.Audience) != 0 {
		return nil, fmt.Errorf("token has restricted audience")
	}
	return claims, nil
}

// PasswordResetNonce derives a short nonce from the first 8 bytes of the
// password hash. When the password changes the nonce changes, automatically
// invalidating any outstanding reset tokens.
func PasswordResetNonce(passwordHash string) string {
	h := sha256.Sum256([]byte(passwordHash))
	return fmt.Sprintf("%x", h[:8])
}

// IssuePasswordReset creates a 1-hour JWT with aud:"password-reset" for the given user.
// The current password hash nonce is embedded so the token is automatically
// invalidated once the password changes.
func (j *JWTIssuer) IssuePasswordReset(userID, orgID, email, passwordHash string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   email, // Stash email in Role field for validation on reset
		Nonce:  PasswordResetNonce(passwordHash),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
			Audience:  jwt.ClaimStrings{"password-reset"},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// ValidatePasswordReset parses a password-reset token, checking aud:"password-reset" and expiry.
func (j *JWTIssuer) ValidatePasswordReset(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return j.secret, nil
	}, jwt.WithAudience("password-reset"))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid password-reset token")
	}
	return claims, nil
}

// IssueAccountDeletion creates a 1-hour JWT with aud:"account-delete" for the given user.
// The current password hash nonce is embedded so the token is automatically
// invalidated once the password changes.
func (j *JWTIssuer) IssueAccountDeletion(userID, orgID, email, passwordHash string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   email, // Stash email in Role field for validation
		Nonce:  PasswordResetNonce(passwordHash),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
			Audience:  jwt.ClaimStrings{"account-delete"},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// ValidateAccountDeletion parses an account-delete token, checking aud:"account-delete" and expiry.
func (j *JWTIssuer) ValidateAccountDeletion(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return j.secret, nil
	}, jwt.WithAudience("account-delete"))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid account-delete token")
	}
	return claims, nil
}

// IssueAccountRecovery creates a 1-hour JWT with aud:"account-recover" for the given user.
// The current password hash nonce is embedded so the token is automatically
// invalidated once the password changes.
func (j *JWTIssuer) IssueAccountRecovery(userID, orgID, email, passwordHash string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   email, // Stash email in Role field for validation
		Nonce:  PasswordResetNonce(passwordHash),
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
			Audience:  jwt.ClaimStrings{"account-recover"},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(j.secret)
}

// ValidateAccountRecovery parses an account-recover token, checking aud:"account-recover" and expiry.
func (j *JWTIssuer) ValidateAccountRecovery(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return j.secret, nil
	}, jwt.WithAudience("account-recover"))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid account-recover token")
	}
	return claims, nil
}
