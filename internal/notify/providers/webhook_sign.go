package providers

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Outbound webhook signing.
//
// Each webhook channel holds a server-generated secret. A delivery carries
//
//	X-YipYap-Timestamp: <unix seconds>
//	X-YipYap-Signature: v1=<hex(HMAC-SHA256(secret, timestamp + "." + body))>
//
// The HMAC key is the secret string exactly as shown to the user (including
// the "whsec_" prefix). The body is the raw request body, byte for byte.
// Receivers should reject timestamps more than WebhookReplayWindow away from
// their own clock. The "v1=" prefix leaves room for a future scheme; a
// receiver must ignore entries in the header with a scheme it does not know.
const (
	WebhookTimestampHeader = "X-YipYap-Timestamp"
	WebhookSignatureHeader = "X-YipYap-Signature"
	WebhookSignatureScheme = "v1"
	WebhookSecretPrefix    = "whsec_"
	WebhookReplayWindow    = 5 * time.Minute
)

// NewWebhookSecret returns a fresh signing secret: "whsec_" followed by 32
// random bytes in unpadded base64url.
func NewWebhookSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return WebhookSecretPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// SignWebhook returns the X-YipYap-Signature header value for body sent at
// unix time ts.
func SignWebhook(secret string, ts int64, body []byte) string {
	return WebhookSignatureScheme + "=" + hex.EncodeToString(webhookMAC(secret, ts, body))
}

func webhookMAC(secret string, ts int64, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return mac.Sum(nil)
}

// VerifyWebhook checks a delivery the way a receiver should: the timestamp
// must be within WebhookReplayWindow of now and one v1 entry in the
// signature header must match. It is the reference implementation for the
// docs and is used by tests.
func VerifyWebhook(secret, timestampHeader, signatureHeader string, body []byte, now time.Time) error {
	ts, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp header")
	}
	skew := now.Sub(time.Unix(ts, 0))
	if skew > WebhookReplayWindow || skew < -WebhookReplayWindow {
		return fmt.Errorf("timestamp outside the %s window", WebhookReplayWindow)
	}
	want := webhookMAC(secret, ts, body)
	for _, part := range strings.Split(signatureHeader, ",") {
		scheme, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || scheme != WebhookSignatureScheme {
			continue
		}
		got, err := hex.DecodeString(value)
		if err != nil {
			continue
		}
		if hmac.Equal(got, want) {
			return nil
		}
	}
	return fmt.Errorf("signature mismatch")
}
