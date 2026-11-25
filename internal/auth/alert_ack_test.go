package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAlertAckRoundTrip(t *testing.T) {
	issuer := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	tok, err := issuer.IssueAlertAck("alert-1", "org-1", "chan-1", "user-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := issuer.ValidateAlertAck(tok)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if c.AlertID != "alert-1" || c.OrgID != "org-1" || c.ChannelID != "chan-1" || c.UserID != "user-1" {
		t.Fatalf("claims = %+v", c)
	}
	if left := time.Until(c.ExpiresAt); left < AlertAckTTL-time.Minute || left > AlertAckTTL {
		t.Fatalf("expiry %v, want about %v", left, AlertAckTTL)
	}
}

func TestAlertAckExpired(t *testing.T) {
	issuer := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	tok, _ := issuer.IssueAlertAck("alert-1", "org-1", "", "", -time.Minute)
	if _, err := issuer.ValidateAlertAck(tok); !errors.Is(err, ErrAlertAckExpired) {
		t.Fatalf("err = %v, want expired", err)
	}
}

func TestAlertAckWrongAudience(t *testing.T) {
	issuer := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	survey, _ := issuer.IssueSurvey("tkt", "user-1", "org-1", time.Hour)
	if _, err := issuer.ValidateAlertAck(survey); err == nil {
		t.Fatal("survey token accepted as an ack link")
	}
	session, _ := issuer.Issue("user-1", "org-1", "owner")
	if _, err := issuer.ValidateAlertAck(session); err == nil {
		t.Fatal("session token accepted as an ack link")
	}
}

func TestAlertAckNotASession(t *testing.T) {
	issuer := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	tok, _ := issuer.IssueAlertAck("alert-1", "org-1", "", "user-1", 0)
	if _, err := issuer.Validate(tok); err == nil {
		t.Fatal("ack link token accepted as a session")
	}
}

func TestAlertAckWrongSecret(t *testing.T) {
	a := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	b := NewJWTIssuer([]byte("other-secret"), time.Hour)
	tok, _ := a.IssueAlertAck("alert-1", "org-1", "", "", 0)
	if _, err := b.ValidateAlertAck(tok); err == nil {
		t.Fatal("token signed with another secret accepted")
	}
}

func TestAlertAckRequiresIDs(t *testing.T) {
	issuer := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	if _, err := issuer.IssueAlertAck("", "org-1", "", "", 0); err == nil {
		t.Fatal("issued a token without an alert ID")
	}
}

func TestAlertAckUUIDsStayShort(t *testing.T) {
	issuer := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	a, o, c, u := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tok, err := issuer.IssueAlertAck(a, o, c, u, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) > 120 {
		t.Errorf("token is %d chars, want at most 120 so SMS links stay short", len(tok))
	}
	got, err := issuer.ValidateAlertAck(tok)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlertID != a || got.OrgID != o || got.ChannelID != c || got.UserID != u {
		t.Fatalf("claims = %+v", got)
	}
}

func TestAlertAckTamperedOrTruncated(t *testing.T) {
	issuer := NewJWTIssuer([]byte("ack-secret"), time.Hour)
	tok, _ := issuer.IssueAlertAck(uuid.NewString(), uuid.NewString(), "", "", 0)
	b := []byte(tok)
	b[8] ^= 1
	if b[8] == '-' || b[8] == '_' { // keep it valid base64url
		b[8] = 'A'
	}
	for name, bad := range map[string]string{
		"flipped":   string(b),
		"truncated": tok[:len(tok)-4],
		"extended":  tok + "AAAA",
		"empty":     "",
		"jwt":       "eyJhbGciOiJIUzI1NiJ9.e30.x",
	} {
		if _, err := issuer.ValidateAlertAck(bad); !errors.Is(err, ErrAlertAckInvalid) {
			t.Errorf("%s: err = %v, want invalid", name, err)
		}
	}
}
