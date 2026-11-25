package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
)

// AlertAckAudience is the audience of signed acknowledge links sent in
// notifications. It is bound into the MAC, so a link token cannot be
// confused with any other token signed by the same secret, and it is not a
// JWT, so Validate never accepts it as a session.
const AlertAckAudience = "alert-ack"

// AlertAckTTL is the lifetime of an acknowledge link. The link handler also
// refuses alerts that are already resolved, so in practice a link stops
// working at whichever comes first.
const AlertAckTTL = 7 * 24 * time.Hour

// Errors returned by ValidateAlertAck.
var (
	ErrAlertAckInvalid = errors.New("invalid alert ack token")
	ErrAlertAckExpired = errors.New("alert ack token expired")
)

// AlertAckClaims identify the alert a link acknowledges and who it was sent
// to.
type AlertAckClaims struct {
	AlertID   string
	OrgID     string
	ChannelID string
	// UserID is the user the notification was addressed to. Empty for
	// channel-wide targets (a shared Slack channel, a team inbox).
	UserID    string
	ExpiresAt time.Time
}

// Token layout, before base64url (no padding):
//
//	version(1) | expires unix seconds (4, big endian) | 4 IDs | mac(16)
//
// Each ID is one length byte then the bytes, except that a canonical UUID
// is written as 0xFF plus its 16 raw bytes. The MAC is HMAC-SHA256 over the
// audience label and everything before it, cut to 128 bits. This keeps a
// link with four UUIDs near 115 characters, about a third of an equivalent
// JWT, which matters in SMS.
const (
	alertAckVersion = 1
	alertAckMACLen  = 16
	alertAckUUIDTag = 0xFF
	alertAckMaxID   = 200
)

// IssueAlertAck signs an acknowledge link token for one alert notification.
// A ttl of zero uses AlertAckTTL.
func (j *JWTIssuer) IssueAlertAck(alertID, orgID, channelID, userID string, ttl time.Duration) (string, error) {
	if alertID == "" || orgID == "" {
		return "", errors.New("alert ack token needs an alert and an org")
	}
	if ttl == 0 {
		ttl = AlertAckTTL
	}
	exp := time.Now().Add(ttl).Unix()
	if exp < 0 || exp > int64(^uint32(0)) {
		return "", errors.New("alert ack expiry out of range")
	}
	buf := make([]byte, 0, 96)
	buf = append(buf, alertAckVersion)
	buf = binary.BigEndian.AppendUint32(buf, uint32(exp))
	for _, id := range []string{alertID, orgID, channelID, userID} {
		var err error
		if buf, err = appendAckID(buf, id); err != nil {
			return "", err
		}
	}
	buf = append(buf, j.alertAckMAC(buf)...)
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidateAlertAck checks the MAC and expiry of an acknowledge link token.
// It returns ErrAlertAckExpired for a genuine token past its expiry and
// ErrAlertAckInvalid for anything else.
func (j *JWTIssuer) ValidateAlertAck(token string) (*AlertAckClaims, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) < 1+4+4+alertAckMACLen || raw[0] != alertAckVersion {
		return nil, ErrAlertAckInvalid
	}
	body, mac := raw[:len(raw)-alertAckMACLen], raw[len(raw)-alertAckMACLen:]
	if !hmac.Equal(mac, j.alertAckMAC(body)) {
		return nil, ErrAlertAckInvalid
	}
	exp := time.Unix(int64(binary.BigEndian.Uint32(body[1:5])), 0)
	rest := body[5:]
	ids := make([]string, 4)
	for i := range ids {
		var ok bool
		if ids[i], rest, ok = readAckID(rest); !ok {
			return nil, ErrAlertAckInvalid
		}
	}
	if len(rest) != 0 || ids[0] == "" || ids[1] == "" {
		return nil, ErrAlertAckInvalid
	}
	if !time.Now().Before(exp) {
		return nil, ErrAlertAckExpired
	}
	return &AlertAckClaims{AlertID: ids[0], OrgID: ids[1], ChannelID: ids[2], UserID: ids[3], ExpiresAt: exp}, nil
}

func (j *JWTIssuer) alertAckMAC(body []byte) []byte {
	m := hmac.New(sha256.New, j.secret)
	m.Write([]byte("yipyap/" + AlertAckAudience + "/v1\x00"))
	m.Write(body)
	return m.Sum(nil)[:alertAckMACLen]
}

func appendAckID(buf []byte, id string) ([]byte, error) {
	if u, err := uuid.Parse(id); err == nil && u.String() == id {
		buf = append(buf, alertAckUUIDTag)
		return append(buf, u[:]...), nil
	}
	if len(id) > alertAckMaxID {
		return nil, errors.New("alert ack ID too long")
	}
	buf = append(buf, byte(len(id)))
	return append(buf, id...), nil
}

func readAckID(b []byte) (string, []byte, bool) {
	if len(b) < 1 {
		return "", nil, false
	}
	n, b := b[0], b[1:]
	if n == alertAckUUIDTag {
		if len(b) < 16 {
			return "", nil, false
		}
		var u uuid.UUID
		copy(u[:], b[:16])
		return u.String(), b[16:], true
	}
	if int(n) > alertAckMaxID || len(b) < int(n) {
		return "", nil, false
	}
	return string(b[:n]), b[n:], true
}
