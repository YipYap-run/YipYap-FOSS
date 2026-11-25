package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// maxAckTokenLen bounds the token before it is decoded. Real
// tokens are about 115 bytes.
const maxAckTokenLen = 2048

// AckLinkHandler serves the signed acknowledge links sent in notifications.
// GET only shows a confirm page, so mail scanners and link previews that
// fetch the URL do not acknowledge anything. POST performs the ack through
// the escalation engine (the same alert.ack path as the chat buttons), which
// writes the timeline entry, emits the CloudEvent, and notifies live views.
type AckLinkHandler struct {
	store  store.Store
	jwt    *auth.JWTIssuer
	bus    bus.Bus
	appURL string
}

// NewAckLinkHandler returns the handler for /ack/{token}. publicBaseURL is
// used only for the "Open YipYap" link on the page.
func NewAckLinkHandler(s store.Store, jwtIssuer *auth.JWTIssuer, b bus.Bus, publicBaseURL string) *AckLinkHandler {
	appURL := strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if appURL == "" {
		appURL = "/"
	}
	return &AckLinkHandler{store: s, jwt: jwtIssuer, bus: b, appURL: appURL}
}

type ackLinkPage struct {
	Title    string
	Message  string
	Monitor  string
	Severity string
	Status   string
	Started  string
	ShowForm bool
	// Button is the submit label when ShowForm is set.
	Button string
	// AppURL is the "open in YipYap" link. It points at the alert when
	// the alert is known, else at the app root. LinkText labels it.
	AppURL   string
	LinkText string
}

// ackLinkState is the outcome of checking a token against current data.
type ackLinkState int

const (
	ackLinkInvalid ackLinkState = iota
	ackLinkExpired
	ackLinkRevoked
	ackLinkResolved
	ackLinkAcked
	ackLinkFiring
)

type ackLinkTarget struct {
	state   ackLinkState
	claims  *auth.AlertAckClaims
	alert   *domain.Alert
	monitor string
	channel string
}

// resolve validates the token and loads the alert it names. The token is
// never logged.
func (h *AckLinkHandler) resolve(ctx context.Context, token string) ackLinkTarget {
	if token == "" || len(token) > maxAckTokenLen {
		return ackLinkTarget{state: ackLinkInvalid}
	}
	claims, err := h.jwt.ValidateAlertAck(token)
	if err != nil {
		if errors.Is(err, auth.ErrAlertAckExpired) {
			return ackLinkTarget{state: ackLinkExpired}
		}
		return ackLinkTarget{state: ackLinkInvalid}
	}
	t := ackLinkTarget{claims: claims}
	alert, err := h.store.Alerts().GetByID(ctx, claims.AlertID)
	if err != nil || alert.OrgID != claims.OrgID {
		t.state = ackLinkInvalid
		return t
	}
	t.alert = alert

	// A link sent to a person stops working once that person is disabled
	// or no longer in the org. A link sent through a channel stops working
	// once the channel is deleted or moved.
	if claims.UserID != "" {
		u, err := h.store.Users().GetByID(ctx, claims.UserID)
		if err != nil || u.OrgID != claims.OrgID || u.DisabledAt != nil {
			t.state = ackLinkRevoked
			return t
		}
	}
	if claims.ChannelID != "" {
		ch, err := h.store.NotificationChannels().GetByID(ctx, claims.ChannelID)
		if err != nil || ch.OrgID != claims.OrgID {
			t.state = ackLinkRevoked
			return t
		}
		t.channel = ch.Name
	}
	if m, err := h.store.Monitors().GetByID(ctx, alert.MonitorID); err == nil && m.OrgID == alert.OrgID {
		t.monitor = m.Name
	}

	switch alert.Status {
	case domain.AlertResolved:
		t.state = ackLinkResolved
	case domain.AlertAcknowledged:
		t.state = ackLinkAcked
	default:
		t.state = ackLinkFiring
	}
	return t
}

// Show handles GET /ack/{token}. It never changes state.
func (h *AckLinkHandler) Show(w http.ResponseWriter, r *http.Request) {
	t := h.resolve(r.Context(), chi.URLParam(r, "token"))
	switch t.state {
	case ackLinkFiring:
		p := h.alertPage(t, "Acknowledge this alert?",
			"Acknowledging tells your team someone is on it and stops further escalation for this alert.")
		p.ShowForm = true
		p.Button = "Acknowledge alert"
		h.render(w, http.StatusOK, p)
	default:
		h.renderState(w, t, false)
	}
}

// Confirm handles POST /ack/{token} from the confirm page form.
func (h *AckLinkHandler) Confirm(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	t := h.resolve(r.Context(), chi.URLParam(r, "token"))
	if t.state != ackLinkFiring {
		h.renderState(w, t, true)
		return
	}

	evt := domain.AckEvent{
		AlertID:     t.alert.ID,
		UserID:      t.claims.UserID,
		Channel:     domain.AckViaLink,
		Via:         domain.AckViaLink,
		ChannelID:   t.claims.ChannelID,
		ChannelName: t.channel,
	}
	data, err := json.Marshal(evt)
	if err == nil {
		err = h.bus.PublishAlert(r.Context(), "alert.ack", data, "")
	}
	if err != nil {
		slog.Error("ack link: publish ack", "alert_id", t.alert.ID, "error", err)
		p := h.alertPage(t, "Could not acknowledge",
			"YipYap could not record the acknowledgement. The alert is still firing. Try again, or open the alert in YipYap.")
		p.ShowForm = true
		p.Button = "Try again"
		h.render(w, http.StatusServiceUnavailable, p)
		return
	}
	p := h.alertPage(t, "Alert acknowledged",
		"Thanks. Your team can see the alert is acknowledged, and escalation stops for this alert.")
	// The alert was loaded before the ack, so show the new status rather
	// than the stale "Firing".
	p.Status = statusLabel(domain.AlertAcknowledged)
	h.render(w, http.StatusOK, p)
}

// renderState renders every non-firing outcome. posted is true for POST, so
// a resolved alert answers 409 to a client that tried to change it.
func (h *AckLinkHandler) renderState(w http.ResponseWriter, t ackLinkTarget, posted bool) {
	switch t.state {
	case ackLinkResolved:
		code := http.StatusOK
		if posted {
			code = http.StatusConflict
		}
		h.render(w, code, h.alertPage(t, "Alert already resolved",
			"This alert is resolved, so there is nothing to acknowledge."))
	case ackLinkAcked:
		h.render(w, http.StatusOK, h.alertPage(t, "Alert already acknowledged",
			"Someone already acknowledged this alert. Escalation has stopped."))
	case ackLinkExpired:
		h.render(w, http.StatusGone, h.plainPage("Link expired",
			"This acknowledge link has expired. Sign in to YipYap to acknowledge the alert."))
	case ackLinkRevoked:
		h.render(w, http.StatusForbidden, h.plainPage("Link no longer valid",
			"This acknowledge link is no longer valid. Sign in to YipYap to acknowledge the alert."))
	default:
		h.render(w, http.StatusNotFound, h.plainPage("Link not valid",
			"This acknowledge link is not valid. Check that the whole link was copied, or sign in to YipYap."))
	}
}

// plainPage is for outcomes where the token did not name a usable alert.
// It links to the app root and shows no alert details, so a bad or
// revoked token reveals nothing.
func (h *AckLinkHandler) plainPage(title, msg string) ackLinkPage {
	return ackLinkPage{Title: title, Message: msg, AppURL: h.appURL, LinkText: "Open YipYap"}
}

func (h *AckLinkHandler) alertPage(t ackLinkTarget, title, msg string) ackLinkPage {
	p := ackLinkPage{Title: title, Message: msg, Monitor: t.monitor,
		AppURL: h.appURL, LinkText: "Open YipYap"}
	if t.alert != nil {
		// The SPA asks for a sign-in first when there is no session, then
		// lands on the alert.
		p.AppURL = strings.TrimRight(h.appURL, "/") + "/alerts/" + url.PathEscape(t.alert.ID)
		p.LinkText = "Open this alert in YipYap"
		p.Severity = severityLabel(t.alert.Severity)
		p.Status = statusLabel(t.alert.Status)
		if !t.alert.StartedAt.IsZero() {
			p.Started = t.alert.StartedAt.UTC().Format("2 Jan 2006, 15:04 UTC")
		}
	}
	return p
}

func severityLabel(s domain.Severity) string {
	switch s {
	case domain.SeverityCritical:
		return "Critical"
	case domain.SeverityWarning:
		return "Warning"
	case domain.SeverityInfo:
		return "Info"
	}
	return string(s)
}

func statusLabel(s domain.AlertStatus) string {
	switch s {
	case domain.AlertFiring:
		return "Firing"
	case domain.AlertAcknowledged:
		return "Acknowledged"
	case domain.AlertResolved:
		return "Resolved"
	}
	return string(s)
}

func (h *AckLinkHandler) render(w http.ResponseWriter, code int, p ackLinkPage) {
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	// The token is in the URL: keep it out of caches, referrers, and
	// search indexes.
	hd.Set("Cache-Control", "no-store")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(code)
	if err := ackLinkTmpl.Execute(w, p); err != nil {
		slog.Warn("ack link: render page", "error", err)
	}
}

// The page has no script, so the strict CSP (script-src 'self') applies
// unchanged. Styles are inline, which the CSP allows. Colors meet WCAG AA
// contrast in both light and dark schemes.
var ackLinkTmpl = template.Must(template.New("ack").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta name="referrer" content="no-referrer">
<title>{{.Title}} | YipYap</title>
<style>
:root{--bg:#f6f7f9;--card:#fff;--text:#1b1f24;--muted:#4b5563;--border:#d0d5dd;--accent:#1d4ed8;--accent-text:#fff;--focus:#1d4ed8}
@media (prefers-color-scheme:dark){:root{--bg:#111418;--card:#1b2027;--text:#eef1f4;--muted:#b4bcc6;--border:#3a424d;--accent:#93b4ff;--accent-text:#0b1220;--focus:#93b4ff}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--text);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:32rem;margin:3rem auto;padding:0 1rem}
.card{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:1.5rem}
h1{font-size:1.375rem;margin:0 0 .75rem}
p{margin:0 0 1rem}
dl{display:grid;grid-template-columns:auto 1fr;gap:.25rem 1rem;margin:0 0 1rem}
dt{color:var(--muted)}
dd{margin:0;overflow-wrap:anywhere}
button{font:inherit;font-weight:600;min-height:44px;padding:.625rem 1.25rem;border:0;border-radius:8px;background:var(--accent);color:var(--accent-text);cursor:pointer}
a{color:var(--accent)}
button:focus-visible,a:focus-visible{outline:3px solid var(--focus);outline-offset:2px}
.brand{font-weight:700;margin:0 0 1rem;color:var(--muted)}
form{margin:0 0 1rem}
.open{margin:0}
@media (max-width:30rem){main{margin:1.5rem auto}button{width:100%}}
</style>
</head>
<body>
<main>
<p class="brand">YipYap</p>
<div class="card">
<h1>{{.Title}}</h1>
{{if or .Monitor .Status}}<dl>
{{if .Monitor}}<dt>Monitor</dt><dd>{{.Monitor}}</dd>{{end}}
{{if .Severity}}<dt>Severity</dt><dd>{{.Severity}}</dd>{{end}}
{{if .Status}}<dt>Status</dt><dd>{{.Status}}</dd>{{end}}
{{if .Started}}<dt>Started</dt><dd>{{.Started}}</dd>{{end}}
</dl>{{end}}
<p>{{.Message}}</p>
{{if .ShowForm}}<form method="post"><button type="submit">{{.Button}}</button></form>{{end}}
{{if .AppURL}}<p class="open"><a href="{{.AppURL}}" rel="noreferrer">{{.LinkText}}</a></p>{{end}}
</div>
</main>
</body>
</html>
`))
