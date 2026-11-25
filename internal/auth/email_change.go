package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Audiences for the two links an email change sends: the confirmation link
// mailed to the new address and the cancel link mailed to the old one.
const (
	EmailChangeConfirmAudience = "email-change"
	EmailChangeCancelAudience  = "email-change-cancel"
)

// EmailChangeTTL is how long both email change links stay valid.
const EmailChangeTTL = 24 * time.Hour

// IssueEmailChange signs a link token for a pending email change. newEmail
// and nonce must match the user's stored pending change when the token is
// redeemed, so a newer request or a finished change voids older links.
func (j *JWTIssuer) IssueEmailChange(audience, userID, orgID, newEmail, nonce string) (string, error) {
	if audience != EmailChangeConfirmAudience && audience != EmailChangeCancelAudience {
		return "", fmt.Errorf("unknown email change audience %q", audience)
	}
	now := time.Now()
	claims := Claims{
		UserID: userID,
		OrgID:  orgID,
		Role:   newEmail, // the pending address, checked on redeem
		Nonce:  nonce,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(EmailChangeTTL)),
			Audience:  jwt.ClaimStrings{audience},
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(j.secret)
}

// ValidateEmailChange parses an email change token for the given audience.
func (j *JWTIssuer) ValidateEmailChange(audience, tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return j.secret, nil
	}, jwt.WithAudience(audience))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid || claims.UserID == "" || claims.Nonce == "" || claims.Role == "" {
		return nil, fmt.Errorf("invalid email change token")
	}
	return claims, nil
}
