package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/coder/websocket"
)

// defaultWSRevalidateInterval is how often an open connection re-runs the
// account checks. A disabled user or one who now owes MFA enrollment or a
// password change loses the live feed within this window.
const defaultWSRevalidateInterval = 30 * time.Second

// wsMessage is the JSON frame sent to WebSocket clients. Types:
//
//   - alert.fired / alert.resolved: the monitor went down or came back up
//     (from the checker's alert.trigger / alert.recover). Status is the
//     monitor's check status.
//   - alert.changed: an alert opened, changed status, escalated or paged
//     someone (from domain.SubjectAlertChanged). AlertID is set and Status
//     is the alert status.
//
// The SPA treats every frame as "something changed" and refreshes the open
// page in the background (web/src/state/live.js).
type wsMessage struct {
	Type        string `json:"type"`
	AlertID     string `json:"alert_id,omitempty"`
	MonitorID   string `json:"monitor_id"`
	MonitorName string `json:"monitor_name"`
	Status      string `json:"status"`
}

// client is a single WebSocket connection scoped to an org.
//
// send is never closed. Shutdown is signalled by closing done (once), so a
// broadcast racing with teardown can at worst queue a frame nobody reads,
// never panic on a closed channel.
type client struct {
	orgID    string
	send     chan []byte
	done     chan struct{}
	stopOnce sync.Once
}

func newClient(orgID string) *client {
	return &client{
		orgID: orgID,
		send:  make(chan []byte, 32),
		done:  make(chan struct{}),
	}
}

// stop signals the write pump to exit. Safe to call more than once.
func (c *client) stop() {
	c.stopOnce.Do(func() { close(c.done) })
}

// Hub manages WebSocket clients and fans out bus events to them.
type Hub struct {
	mu            sync.RWMutex
	clients       map[*client]struct{}
	byOrg         map[string]map[*client]struct{}
	allowedOrigin string // derived from publicBaseURL

	// revalidateEvery is the interval between account re-checks on an open
	// connection. Tests shorten it.
	revalidateEvery time.Duration
}

// NewHub creates an empty hub. publicBaseURL is used to validate WebSocket
// upgrade request origins; pass an empty string to allow any origin (not
// recommended for production).
func NewHub(publicBaseURL string) *Hub {
	origin := ""
	if publicBaseURL != "" {
		if u, err := url.Parse(publicBaseURL); err == nil && u.Host != "" {
			origin = u.Scheme + "://" + u.Host
		}
	}
	return &Hub{
		clients:         make(map[*client]struct{}),
		byOrg:           make(map[string]map[*client]struct{}),
		allowedOrigin:   origin,
		revalidateEvery: defaultWSRevalidateInterval,
	}
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
	if h.byOrg[c.orgID] == nil {
		h.byOrg[c.orgID] = make(map[*client]struct{})
	}
	h.byOrg[c.orgID][c] = struct{}{}
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	if orgClients, ok := h.byOrg[c.orgID]; ok {
		delete(orgClients, c)
		if len(orgClients) == 0 {
			delete(h.byOrg, c.orgID)
		}
	}
}

// broadcast sends data to all clients in the given org. Slow clients that
// can't keep up have their message dropped (non-blocking send).
//
// The org's clients are copied into a slice under the read lock. Ranging the
// live map after unlocking races with register/unregister and the runtime
// aborts on concurrent map iteration and write, which recover cannot catch.
func (h *Hub) broadcast(orgID string, data []byte) {
	h.mu.RLock()
	targets := make([]*client, 0, len(h.byOrg[orgID]))
	for c := range h.byOrg[orgID] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	// broadcast runs inside bus callbacks that have no recover of their own
	// (core NATS subscriptions). Never let a fan-out bug take the process down.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("ws hub: broadcast panic", "panic", r)
		}
	}()

	for _, c := range targets {
		select {
		case <-c.done:
			// Disconnected after the snapshot was taken.
		case c.send <- data:
		default:
			// Client too slow: drop the message rather than block.
		}
	}
}

// SubscribeToBus wires the hub to the message bus. It subscribes as a
// broadcast listener (not a queue group) so existing escalation engine
// processing is unaffected.
func (h *Hub) SubscribeToBus(b bus.Bus) error {
	handle := func(_ context.Context, subject string, data []byte) error {
		var evt checker.AlertEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			slog.Error("ws hub: unmarshal alert event", "error", err)
			return nil
		}

		var msgType string
		switch subject {
		case bus.AlertBroadcastSubject("alert.trigger"):
			msgType = "alert.fired"
		case bus.AlertBroadcastSubject("alert.recover"):
			msgType = "alert.resolved"
		default:
			return nil
		}

		msg := wsMessage{
			Type:        msgType,
			MonitorID:   evt.MonitorID,
			MonitorName: evt.MonitorName,
			Status:      string(evt.Status),
		}
		frame, err := json.Marshal(msg)
		if err != nil {
			return nil
		}
		h.broadcast(evt.OrgID, frame)
		return nil
	}

	// Listen on the broadcast subjects, not the durable ones: on JetStream
	// the durable subjects belong to the ALERTS stream. (core-16)
	if err := b.Subscribe(bus.AlertBroadcastSubject("alert.trigger"), handle); err != nil {
		return err
	}
	if err := b.Subscribe(bus.AlertBroadcastSubject("alert.recover"), handle); err != nil {
		return err
	}
	if err := b.Subscribe(domain.SubjectAlertChanged, h.handleAlertChanged); err != nil {
		return err
	}
	// The escalation engine broadcasts alert.acknowledged after it stores
	// an ack from any source (chat button, signed link, Events API).
	return b.Subscribe("alert.acknowledged", func(_ context.Context, _ string, data []byte) error {
		var m domain.AlertAckedMessage
		if err := json.Unmarshal(data, &m); err != nil || m.OrgID == "" {
			return nil
		}
		frame, err := json.Marshal(wsMessage{
			Type:        "alert.acknowledged",
			MonitorID:   m.MonitorID,
			MonitorName: m.MonitorName,
			Status:      m.Status,
			AlertID:     m.AlertID,
		})
		if err != nil {
			return nil
		}
		h.broadcast(m.OrgID, frame)
		return nil
	})
}

// handleAlertChanged relays an alert.changed hint to the alert's org.
func (h *Hub) handleAlertChanged(_ context.Context, _ string, data []byte) error {
	var evt domain.AlertChanged
	if err := json.Unmarshal(data, &evt); err != nil {
		slog.Error("ws hub: unmarshal alert.changed", "error", err)
		return nil
	}
	if evt.OrgID == "" {
		return nil
	}
	frame, err := json.Marshal(wsMessage{
		Type:      "alert.changed",
		AlertID:   evt.AlertID,
		MonitorID: evt.MonitorID,
		Status:    string(evt.Status),
	})
	if err != nil {
		return nil
	}
	h.broadcast(evt.OrgID, frame)
	return nil
}

// wsAccessError is a refused account check, carrying the status and message
// the matching API middleware would have returned.
type wsAccessError struct {
	status int
	body   string
}

func (e *wsAccessError) Error() string { return e.body }

// checkAccess runs the same account gates as authenticated API routes
// (disabled account, forced password change, org MFA enforcement) against
// claims. It reuses the middleware so /ws can never drift from the API.
func checkAccess(ctx context.Context, s store.Store, claims *auth.Claims) error {
	var passed bool
	final := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { passed = true })
	chain := middleware.DisabledAccountGuard(s)(
		middleware.ForcePasswordChange(s)(
			middleware.MFAEnforce(s)(final)))

	req, err := http.NewRequestWithContext(middleware.SetClaims(ctx, claims), http.MethodGet, "/ws", nil)
	if err != nil {
		return err
	}
	rec := &captureWriter{header: make(http.Header)}
	chain.ServeHTTP(rec, req)
	if passed {
		return nil
	}
	status := rec.status
	if status == 0 {
		status = http.StatusForbidden
	}
	return &wsAccessError{status: status, body: rec.body}
}

// captureWriter is a minimal ResponseWriter that records what a refusing
// middleware wrote so the result can be replayed or logged.
type captureWriter struct {
	header http.Header
	status int
	body   string
}

func (w *captureWriter) Header() http.Header { return w.header }
func (w *captureWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *captureWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.body += string(b)
	return len(b), nil
}

// wsSessionRecheck is how often an open socket re-checks its session, so a
// revoked session's socket closes within this interval plus the session
// cache TTL.
var wsSessionRecheck = 15 * time.Second

// ServeWS is the HTTP handler for the /ws endpoint. It authenticates with
// the HttpOnly session cookie only (a token in the query string would end
// up in proxy logs), runs the same account gates as the API, upgrades, and
// then re-checks the account periodically, closing the connection when the
// user is disabled, owes MFA or a password change, the session is revoked,
// or the session token expires.
func (h *Hub) ServeWS(jwt *auth.JWTIssuer, s store.Store, checkSession middleware.SessionCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("yipyap_session")
		if err != nil || cookie.Value == "" {
			http.Error(w, "missing session", http.StatusUnauthorized)
			return
		}

		claims, err := jwt.Validate(cookie.Value)
		if err != nil {
			http.Error(w, "invalid session", http.StatusUnauthorized)
			return
		}

		if err := checkAccess(r.Context(), s, claims); err != nil {
			status := http.StatusForbidden
			body := `{"error":"access denied"}`
			if ae, ok := err.(*wsAccessError); ok {
				status = ae.status
				if ae.body != "" {
					body = ae.body
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		if checkSession != nil {
			if err := checkSession(r.Context(), claims); err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
		}

		acceptOpts := &websocket.AcceptOptions{}
		if h.allowedOrigin != "" {
			acceptOpts.OriginPatterns = []string{h.allowedOrigin}
		} else {
			// No origin configured, fall back to insecure (dev/test only).
			acceptOpts.InsecureSkipVerify = true
		}
		conn, err := websocket.Accept(w, r, acceptOpts)
		if err != nil {
			slog.Error("ws accept failed", "error", err)
			return
		}

		ctx, cancel := context.WithCancel(r.Context())
		c := newClient(claims.OrgID)
		h.register(c)
		// Teardown order: unregister first so no new broadcast can pick
		// this client up, then signal the write pump, then cancel.
		defer func() {
			h.unregister(c)
			c.stop()
			cancel()
			_ = conn.CloseNow()
		}()

		// Session watcher: close the socket once the session is revoked.
		if checkSession != nil {
			go func() {
				t := time.NewTicker(wsSessionRecheck)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						if err := checkSession(ctx, claims); errors.Is(err, session.ErrRevoked) {
							_ = conn.Close(websocket.StatusPolicyViolation, "session revoked")
							return
						}
					}
				}
			}()
		}

		// Write pump: sends queued messages to the client.
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-c.done:
					return
				case msg := <-c.send:
					writeCtx, wcancel := context.WithTimeout(ctx, 5*time.Second)
					err := conn.Write(writeCtx, websocket.MessageText, msg)
					wcancel()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()

		// Revalidation: re-run the account gates on a timer and close once
		// the session token itself expires.
		go h.revalidate(ctx, conn, s, claims, cancel)

		// Read pump: blocks until the client disconnects or ctx is
		// cancelled. We don't expect any client-to-server messages but
		// must read to detect close frames.
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return
			}
		}
	}
}

// revalidate closes conn (via cancel and a policy-violation close frame)
// when the account stops passing checkAccess or the token expires.
func (h *Hub) revalidate(ctx context.Context, conn *websocket.Conn, s store.Store, claims *auth.Claims, cancel context.CancelFunc) {
	interval := h.revalidateEvery
	if interval <= 0 {
		interval = defaultWSRevalidateInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var expiry <-chan time.Time
	if claims.ExpiresAt != nil {
		t := time.NewTimer(time.Until(claims.ExpiresAt.Time))
		defer t.Stop()
		expiry = t.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-expiry:
			_ = conn.Close(websocket.StatusPolicyViolation, "session expired")
			cancel()
			return
		case <-ticker.C:
			if err := checkAccess(ctx, s, claims); err != nil {
				if ctx.Err() != nil {
					return
				}
				// A store outage (5xx) is not a verdict on the account;
				// keep the connection and try again next tick.
				if ae, ok := err.(*wsAccessError); ok && ae.status >= http.StatusInternalServerError {
					continue
				}
				_ = conn.Close(websocket.StatusPolicyViolation, "access revoked")
				cancel()
				return
			}
		}
	}
}
