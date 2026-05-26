package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// BulkImportHandler handles bulk resource import.
type BulkImportHandler struct {
	store store.Store
}

// NewBulkImportHandler creates a new BulkImportHandler.
func NewBulkImportHandler(s store.Store) *BulkImportHandler {
	return &BulkImportHandler{store: s}
}

type monitorImport struct {
	Name               string            `json:"name"`
	Type               string            `json:"type"`
	Config             json.RawMessage   `json:"config"`
	IntervalSeconds    int               `json:"interval_seconds"`
	TimeoutSeconds     int               `json:"timeout_seconds"`
	Description        string            `json:"description"`
	Enabled            *bool             `json:"enabled"`
	Labels             map[string]string `json:"labels"`
	EscalationPolicyID string            `json:"escalation_policy_id"`
	GroupID            string            `json:"group_id"`
}

type teamImport struct {
	Name string `json:"name"`
}

type escalationImport struct {
	Name string `json:"name"`
}

type channelImport struct {
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config"`
}

// BulkImportRequest is the top-level request body for bulk import.
type BulkImportRequest struct {
	Monitors             []monitorImport    `json:"monitors"`
	Teams                []teamImport       `json:"teams"`
	EscalationPolicies   []escalationImport `json:"escalation_policies"`
	NotificationChannels []channelImport    `json:"notification_channels"`
}

type importSummary struct {
	Created map[string]int `json:"created"`
	Errors  []string       `json:"errors"`
}

// Import handles POST /api/v1/import.
func (h *BulkImportHandler) Import(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())

	var req BulkImportRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate all resources first.
	var errors []string
	for i, ch := range req.NotificationChannels {
		if ch.Name == "" {
			errors = append(errors, fmt.Sprintf("notification_channels[%d]: name is required", i))
		}
		if ch.Type == "" {
			errors = append(errors, fmt.Sprintf("notification_channels[%d]: type is required", i))
		}
	}
	for i, t := range req.Teams {
		if t.Name == "" {
			errors = append(errors, fmt.Sprintf("teams[%d]: name is required", i))
		}
	}
	for i, ep := range req.EscalationPolicies {
		if ep.Name == "" {
			errors = append(errors, fmt.Sprintf("escalation_policies[%d]: name is required", i))
		}
	}
	for i, m := range req.Monitors {
		if m.Name == "" {
			errors = append(errors, fmt.Sprintf("monitors[%d]: name is required", i))
		}
		if m.Type == "" {
			errors = append(errors, fmt.Sprintf("monitors[%d]: type is required", i))
		}
		validTypes := map[string]bool{"http": true, "tcp": true, "ping": true, "dns": true, "heartbeat": true}
		if m.Type != "" && !validTypes[m.Type] {
			errors = append(errors, fmt.Sprintf("monitors[%d]: invalid type %q", i, m.Type))
		}
	}
	if len(errors) > 0 {
		jsonResponse(w, http.StatusBadRequest, importSummary{
			Created: map[string]int{},
			Errors:  errors,
		})
		return
	}

	summary := importSummary{
		Created: map[string]int{
			"notification_channels": 0,
			"teams":                0,
			"escalation_policies":  0,
			"monitors":             0,
		},
		Errors: []string{},
	}

	now := time.Now().UTC().Truncate(time.Second)

	// Create in dependency order: channels -> teams -> policies -> monitors.

	// 1. Notification Channels.
	for i, ch := range req.NotificationChannels {
		configStr := ""
		if ch.Config != nil {
			configStr = string(ch.Config)
		}
		nc := &domain.NotificationChannel{
			ID:     uuid.New().String(),
			OrgID:  claims.OrgID,
			Name:   ch.Name,
			Type:   ch.Type,
			Config: configStr,
		}
		if err := h.store.NotificationChannels().Create(r.Context(), nc); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("notification_channels[%d] %q: %v", i, ch.Name, err))
			continue
		}
		summary.Created["notification_channels"]++
	}

	// 2. Teams.
	for i, t := range req.Teams {
		team := &domain.Team{
			ID:    uuid.New().String(),
			OrgID: claims.OrgID,
			Name:  t.Name,
		}
		if err := h.store.Teams().Create(r.Context(), team); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("teams[%d] %q: %v", i, t.Name, err))
			continue
		}
		summary.Created["teams"]++
	}

	// 3. Escalation Policies.
	for i, ep := range req.EscalationPolicies {
		policy := &domain.EscalationPolicy{
			ID:    uuid.New().String(),
			OrgID: claims.OrgID,
			Name:  ep.Name,
		}
		if err := h.store.EscalationPolicies().Create(r.Context(), policy); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("escalation_policies[%d] %q: %v", i, ep.Name, err))
			continue
		}
		summary.Created["escalation_policies"]++
	}

	// 4. Monitors.
	for i, m := range req.Monitors {
		// SSRF validation: reject private/internal targets.
		if err := validateMonitorTarget(domain.MonitorType(m.Type), m.Config); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("monitors[%d]: %v", i, err))
			continue
		}
		// Validate that escalation policy belongs to this org.
		if m.EscalationPolicyID != "" {
			ep, err := h.store.EscalationPolicies().GetByID(r.Context(), m.EscalationPolicyID)
			if err != nil || ep.OrgID != claims.OrgID {
				summary.Errors = append(summary.Errors, fmt.Sprintf("monitors[%d]: invalid escalation_policy_id", i))
				continue
			}
		}
		// Validate that group belongs to this org.
		if m.GroupID != "" {
			if mgp, ok := h.store.(store.MonitorGroupProvider); ok {
				grp, err := mgp.MonitorGroups().GetByID(r.Context(), m.GroupID)
				if err != nil || grp.OrgID != claims.OrgID {
					summary.Errors = append(summary.Errors, fmt.Sprintf("monitors[%d]: invalid group_id", i))
					continue
				}
			}
		}
		enabled := true
		if m.Enabled != nil {
			enabled = *m.Enabled
		}
		intervalSec := m.IntervalSeconds
		if intervalSec <= 0 {
			intervalSec = 60
		}
		timeoutSec := m.TimeoutSeconds
		if timeoutSec <= 0 {
			timeoutSec = 10
		}
		mon := &domain.Monitor{
			ID:                 uuid.New().String(),
			OrgID:              claims.OrgID,
			Name:               m.Name,
			Type:               domain.MonitorType(m.Type),
			Config:             m.Config,
			IntervalSeconds:    intervalSec,
			TimeoutSeconds:     timeoutSec,
			Description:        m.Description,
			Enabled:            enabled,
			EscalationPolicyID: m.EscalationPolicyID,
			GroupID:            m.GroupID,
			AutoResolve:        true,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		if err := h.store.Monitors().Create(r.Context(), mon); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("monitors[%d] %q: %v", i, m.Name, err))
			continue
		}
		if len(m.Labels) > 0 {
			_ = h.store.Monitors().SetLabels(r.Context(), mon.ID, m.Labels)
		}
		summary.Created["monitors"]++
	}

	jsonResponse(w, http.StatusOK, summary)
}
