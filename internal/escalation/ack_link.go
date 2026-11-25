package escalation

import (
	"log/slog"
	"net/url"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

// AckLinkFunc returns the signed acknowledge URL for one notification, or ""
// when no link can be built. The notifier leaves the ack line out of a
// message when the URL is empty.
type AckLinkFunc func(alertID, orgID, channelID, userID string) string

// SetAckLinker attaches the function that builds acknowledge links for
// notification jobs. A nil linker (the default) sends no links. Must be
// called before Start.
func (e *Engine) SetAckLinker(fn AckLinkFunc) {
	e.ackLink = fn
}

// SignedAckLinks returns an AckLinkFunc that signs a token with issuer and
// points it at {publicBaseURL}/ack/{token}. It returns nil when the base
// URL is empty or not an absolute http(s) URL, because a relative link in
// an SMS or email would not work.
func SignedAckLinks(issuer *auth.JWTIssuer, publicBaseURL string) AckLinkFunc {
	base := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	u, err := url.Parse(base)
	if issuer == nil || base == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil
	}
	return func(alertID, orgID, channelID, userID string) string {
		tok, err := issuer.IssueAlertAck(alertID, orgID, channelID, userID, 0)
		if err != nil {
			slog.Warn("escalation: sign ack link", "alert_id", alertID, "error", err)
			return ""
		}
		return base + "/ack/" + tok
	}
}
