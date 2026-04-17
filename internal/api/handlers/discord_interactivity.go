package handlers

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// DiscordInteractivityHandler handles Discord interaction callbacks
// (button clicks for acknowledge/resolve).
type DiscordInteractivityHandler struct {
	store     store.Store
	bus       bus.Bus
	publicKey ed25519.PublicKey // Ed25519 public key for signature verification (nil disables verification)
}

// NewDiscordInteractivityHandler creates a new DiscordInteractivityHandler.
// publicKeyHex is the hex-encoded Ed25519 public key from the Discord app's
// General Information page. If empty or invalid, all requests are rejected
// (fail closed).
func NewDiscordInteractivityHandler(s store.Store, b bus.Bus, publicKeyHex string) *DiscordInteractivityHandler {
	h := &DiscordInteractivityHandler{store: s, bus: b}
	if publicKeyHex != "" {
		key, err := hex.DecodeString(publicKeyHex)
		if err != nil || len(key) != ed25519.PublicKeySize {
			slog.Warn("discord: invalid public key, all requests will be rejected", "error", err)
		} else {
			h.publicKey = ed25519.PublicKey(key)
			slog.Info("discord: Ed25519 signature verification enabled")
		}
	}
	return h
}

// discordInteraction mirrors the relevant fields of a Discord interaction callback.
type discordInteraction struct {
	Type   int `json:"type"`
	Member struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
	} `json:"member"`
	Data struct {
		CustomID      string `json:"custom_id"`
		ComponentType int    `json:"component_type"`
	} `json:"data"`
}

// Handle processes POST /webhooks/discord.
//
// Discord sends two types of interactions we care about:
//   - Type 1 (PING): verification ping that must be answered with {"type": 1}.
//   - Type 3 (MESSAGE_COMPONENT): button clicks containing data.custom_id.
//
// All requests are verified using Ed25519 signatures when a public key is configured.
func (h *DiscordInteractivityHandler) Handle(w http.ResponseWriter, r *http.Request) {
	// Read the body for both signature verification and JSON parsing.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Fail closed if no public key is configured.
	if h.publicKey == nil {
		slog.Warn("discord interactivity: webhook verification not configured, rejecting request")
		http.Error(w, "Discord webhook verification not configured", http.StatusServiceUnavailable)
		return
	}

	// Verify Ed25519 signature.
	signature := r.Header.Get("X-Signature-Ed25519")
	timestamp := r.Header.Get("X-Signature-Timestamp")
	if !h.verifySignature(signature, timestamp, body) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var interaction discordInteraction
	if err := json.Unmarshal(body, &interaction); err != nil {
		slog.Warn("discord interactivity: invalid JSON body", "error", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":1}`))
		return
	}

	// Type 1: PING  - respond with PONG for webhook verification.
	if interaction.Type == 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"type":1}`))
		return
	}

	// Type 3: MESSAGE_COMPONENT  - button click.
	if interaction.Type == 3 {
		h.handleComponent(w, r, &interaction)
		return
	}

	// Unknown interaction type  - acknowledge silently.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"type":1}`))
}

func (h *DiscordInteractivityHandler) handleComponent(w http.ResponseWriter, r *http.Request, interaction *discordInteraction) {
	customID := interaction.Data.CustomID
	if customID == "" {
		slog.Warn("discord interactivity: empty custom_id")
		h.respondDeferred(w)
		return
	}

	parts := strings.SplitN(customID, ":", 2)
	if len(parts) != 2 {
		slog.Warn("discord interactivity: invalid custom_id format", "custom_id", customID)
		h.respondDeferred(w)
		return
	}

	action := parts[0]
	alertID := parts[1]

	switch action {
	case "ack":
		ackEvt := domain.AckEvent{
			AlertID: alertID,
			UserID:  interaction.Member.User.ID,
			Channel: "discord",
		}
		data, err := json.Marshal(ackEvt)
		if err != nil {
			slog.Error("discord interactivity: marshal ack event", "error", err)
			h.respondDeferred(w)
			return
		}
		if err := h.bus.Publish(r.Context(), "alert.ack", data); err != nil {
			slog.Error("discord interactivity: publish ack", "error", err)
		}

	case "resolve":
		alert, err := h.store.Alerts().GetByID(r.Context(), alertID)
		if err != nil {
			slog.Warn("discord interactivity: alert not found", "alert_id", alertID, "error", err)
			h.respondDeferred(w)
			return
		}
		monitor, err := h.store.Monitors().GetByID(r.Context(), alert.MonitorID)
		if err != nil {
			slog.Warn("discord interactivity: monitor not found", "monitor_id", alert.MonitorID, "error", err)
			h.respondDeferred(w)
			return
		}
		evt := alertTriggerEvent{
			MonitorID:   monitor.ID,
			MonitorName: monitor.Name,
			Status:      domain.StatusUp,
			LatencyMS:   0,
			CheckedAt:   time.Now().UTC(),
		}
		data, err := json.Marshal(evt)
		if err != nil {
			slog.Error("discord interactivity: marshal recover event", "error", err)
			h.respondDeferred(w)
			return
		}
		if err := h.bus.Publish(r.Context(), "alert.recover", data); err != nil {
			slog.Error("discord interactivity: publish recover", "error", err)
		}

	default:
		slog.Warn("discord interactivity: unknown action", "action", action)
	}

	h.respondDeferred(w)
}

// verifySignature checks the Ed25519 signature on a Discord interaction request.
func (h *DiscordInteractivityHandler) verifySignature(signatureHex, timestamp string, body []byte) bool {
	if signatureHex == "" || timestamp == "" {
		return false
	}
	sig, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}
	msg := append([]byte(timestamp), body...)
	return ed25519.Verify(h.publicKey, msg, sig)
}

// respondDeferred sends a DEFERRED_UPDATE_MESSAGE response (type 6) to Discord,
// acknowledging the interaction without updating the message.
func (h *DiscordInteractivityHandler) respondDeferred(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"type":6}`))
}
