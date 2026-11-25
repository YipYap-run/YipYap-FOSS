package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// slackMaxSkew is how far X-Slack-Request-Timestamp may be from now.
const slackMaxSkew = 5 * time.Minute

var errSlackSignature = errors.New("slack signature check failed")

// readVerifiedSlackRequest reads the body (up to 1 MiB) and checks Slack's
// v0 request signature against signingSecret. An empty secret rejects every
// request. The caller restores r.Body if it needs to parse it again.
func readVerifiedSlackRequest(r *http.Request, signingSecret string, now time.Time) ([]byte, error) {
	if signingSecret == "" {
		return nil, errors.New("no slack signing secret configured")
	}
	rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	tsStr := r.Header.Get("X-Slack-Request-Timestamp")
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return nil, errSlackSignature
	}
	if now.Sub(time.Unix(ts, 0)).Abs() > slackMaxSkew {
		return nil, errors.New("slack request timestamp outside the allowed window")
	}
	sig := r.Header.Get("X-Slack-Signature")
	if sig == "" {
		return nil, errSlackSignature
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte("v0:" + tsStr + ":"))
	mac.Write(rawBody)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return nil, errSlackSignature
	}
	return rawBody, nil
}
