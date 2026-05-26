package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// wsMessage is the JSON frame sent to WebSocket clients. The "type" field
// matches the frontend's expected values in web/src/pages/dashboard/index.jsx.
type wsMessage struct {
	Type        string `json:"type"`
	MonitorID   string `json:"monitor_id"`
	MonitorName string `json:"monitor_name"`
	Status      string `json:"status"`
}

// client is a single WebSocket connection scoped to an org.
type client struct {
	orgID string
	send  chan []byte
}

// Hub manages WebSocket clients and fans out bus events to them.
type Hub struct {
	mu            sync.RWMutex
	clients       map[*client]struct{}
	byOrg         map[string]map[*client]struct{}
	allowedOrigin string // derived from publicBaseURL
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
		clients:       make(map[*client]struct{}),
		byOrg:         make(map[string]map[*client]struct{}),
		allowedOrigin: origin,
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
func (h *Hub) broadcast(orgID string, data []byte) {
	h.mu.RLock()
	orgClients := h.byOrg[orgID]
	h.mu.RUnlock()

	for c := range orgClients {
		select {
		case c.send <- data:
		default:
			// Client too slow  - drop the message rather than block.
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
		case "alert.trigger":
			msgType = "alert.fired"
		case "alert.recover":
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

	if err := b.Subscribe("alert.trigger", handle); err != nil {
		return err
	}
	return b.Subscribe("alert.recover", handle)
}

// ServeWS is the HTTP handler for the /ws endpoint. It validates the JWT
// from the session cookie (preferred) or the ?token= query parameter
// (backwards-compatible fallback), upgrades to WebSocket, and manages the
// connection. Using the cookie avoids the token being logged by proxies.
func (h *Hub) ServeWS(jwt *auth.JWTIssuer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Prefer the HttpOnly session cookie  - the WebSocket upgrade request
		// sends cookies automatically, so no JS-visible token is required.
		var token string
		if cookie, err := r.Cookie("yipyap_session"); err == nil && cookie.Value != "" {
			token = cookie.Value
		}
		// Fall back to the query parameter for backwards compatibility.
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		if token == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}

		claims, err := jwt.Validate(token)
		if err != nil {
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}

		acceptOpts := &websocket.AcceptOptions{}
		if h.allowedOrigin != "" {
			acceptOpts.OriginPatterns = []string{h.allowedOrigin}
		} else {
			// No origin configured  - fall back to insecure (dev/test only).
			acceptOpts.InsecureSkipVerify = true
		}
		conn, err := websocket.Accept(w, r, acceptOpts)
		if err != nil {
			slog.Error("ws accept failed", "error", err)
			return
		}

		c := &client{
			orgID: claims.OrgID,
			send:  make(chan []byte, 32),
		}
		h.register(c)
		defer h.unregister(c)

		ctx := r.Context()

		// Write pump: sends queued messages to the client.
		go func() {
			defer func() { _ = conn.CloseNow() }()
			for {
				select {
				case <-ctx.Done():
					return
				case msg, ok := <-c.send:
					if !ok {
						return
					}
					writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					err := conn.Write(writeCtx, websocket.MessageText, msg)
					cancel()
					if err != nil {
						return
					}
				}
			}
		}()

		// Read pump: blocks until the client disconnects. We don't expect
		// any client-to-server messages but must read to detect close frames.
		for {
			_, _, err := conn.Read(ctx)
			if err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "")
				close(c.send)
				return
			}
		}
	}
}
