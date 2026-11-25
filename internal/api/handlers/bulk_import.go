package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

// BulkImportHandler handles bulk resource import.
type BulkImportHandler struct {
	store    store.Store
	channels *NotificationChannelHandler
	onChange func(kind MonitorChangeKind, monitorID string)
}

// NewBulkImportHandler creates a new BulkImportHandler. platformEmail says
// whether the server has a platform mailer, so an imported email channel is
// held to the same rules as one made in the form.
func NewBulkImportHandler(s store.Store, platformEmail bool) *BulkImportHandler {
	channels := NewNotificationChannelHandler(s, nil)
	channels.SetPlatformEmail(platformEmail)
	return &BulkImportHandler{store: s, channels: channels}
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
	Name        string `json:"name"`
	Description string `json:"description"`
}

type channelImport struct {
	Name    string          `json:"name"`
	Type    string          `json:"type"`
	Config  json.RawMessage `json:"config"`
	Enabled *bool           `json:"enabled"`
}

// BulkImportRequest is the top-level request body for bulk import.
type BulkImportRequest struct {
	Monitors             []monitorImport    `json:"monitors"`
	Teams                []teamImport       `json:"teams"`
	EscalationPolicies   []escalationImport `json:"escalation_policies"`
	NotificationChannels []channelImport    `json:"notification_channels"`
}

// maxImportResources caps one import request. Each monitor target is
// resolved during validation, so an unbounded batch would tie up a request
// worker. Larger setups import in several requests.
const maxImportResources = 1000

type importSummary struct {
	Created map[string]int `json:"created"`
	Errors  []string       `json:"errors"`
	// Error and UpgradeRequired are set only when the whole import is
	// rejected, so the SPA client can show the reason and the upgrade
	// prompt for plan rejections.
	Error           string `json:"error,omitempty"`
	UpgradeRequired bool   `json:"upgrade_required,omitempty"`
}

// SetOnChange registers the callback single create uses, so imported
// monitors are scheduled right away.
func (h *BulkImportHandler) SetOnChange(fn func(kind MonitorChangeKind, monitorID string)) {
	h.onChange = fn
}

// Import handles POST /api/v1/import.
//
// Every resource goes through the same checks as its single-create
// endpoint: plan limits (counting the whole batch), paid-only channel
// types, SSRF and phone checks on channel config, and for monitors the
// shared prepareNewMonitor (type, target, interval floor, org ownership of
// referenced IDs, heartbeat token). Validation is all or nothing: if any
// resource fails, nothing is created.
func (h *BulkImportHandler) Import(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	ctx := r.Context()

	var raw json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var req BulkImportRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body: "+importDecodeHint(err))
		return
	}
	if errs := importShapeErrors(raw); len(errs) > 0 {
		jsonResponse(w, http.StatusBadRequest, importSummary{
			Created: map[string]int{},
			Errors:  errs,
			Error:   "Import rejected, nothing was created. " + errs[0],
		})
		return
	}

	total := len(req.Monitors) + len(req.Teams) + len(req.EscalationPolicies) + len(req.NotificationChannels)
	if total == 0 {
		errorResponse(w, http.StatusBadRequest,
			"the import is empty. Add monitors, teams, escalation_policies or notification_channels")
		return
	}
	if total > maxImportResources {
		errorResponse(w, http.StatusBadRequest, fmt.Sprintf(
			"an import can hold at most %d resources (got %d). Split it into several imports", maxImportResources, total))
		return
	}

	org, err := h.store.Orgs().GetByID(ctx, claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to get org")
		return
	}

	var errs []string
	planRejected := false
	upgrade := false
	addErr := func(prefix string, err error) {
		if isPlanError(err) {
			planRejected = true
			upgrade = upgrade || upgradeApplies(err)
		}
		errs = append(errs, prefix+": "+err.Error())
	}

	// Plan limits, counting the whole batch.
	if n := len(req.NotificationChannels); n > 0 {
		if err := checkChannelLimitN(ctx, h.store, claims.OrgID, n); err != nil {
			addErr("notification_channels", err)
		}
	}
	if n := len(req.Teams); n > 0 {
		if err := checkTeamLimitN(ctx, h.store, claims.OrgID, n); err != nil {
			addErr("teams", err)
		}
	}
	if n := len(req.EscalationPolicies); n > 0 {
		if err := checkEscalationPolicyLimitN(ctx, h.store, claims.OrgID, n); err != nil {
			addErr("escalation_policies", err)
		}
	}
	if n := len(req.Monitors); n > 0 {
		if err := checkMonitorLimitN(ctx, h.store, claims.OrgID, org, n); err != nil {
			addErr("monitors", err)
		}
	}

	for i, ch := range req.NotificationChannels {
		prefix := fmt.Sprintf("notification_channels[%d]", i)
		if ch.Name == "" {
			addErr(prefix, fmt.Errorf("name is required"))
		}
		if ch.Type == "" {
			addErr(prefix, fmt.Errorf("type is required"))
			continue
		}
		if err := channelPlanGateError(ctx, h.store, claims.OrgID, ch.Type); err != nil {
			addErr(prefix, err)
		}
	}
	for i, t := range req.Teams {
		if t.Name == "" {
			addErr(fmt.Sprintf("teams[%d]", i), fmt.Errorf("name is required"))
		}
	}
	for i, ep := range req.EscalationPolicies {
		// Same checks as a single create; keep the trimmed values.
		p := domain.EscalationPolicy{Name: ep.Name, Description: ep.Description}
		errs, order := cleanPolicyFields(&p)
		for _, f := range order {
			addErr(fmt.Sprintf("escalation_policies[%d]", i), fmt.Errorf("%s", errs[f]))
		}
		req.EscalationPolicies[i] = escalationImport{Name: p.Name, Description: p.Description}
	}

	now := time.Now().UTC().Truncate(time.Second)
	monitors := make([]*domain.Monitor, 0, len(req.Monitors))
	for i, m := range req.Monitors {
		prefix := fmt.Sprintf("monitors[%d]", i)
		if m.Name == "" {
			addErr(prefix, fmt.Errorf("name is required"))
		}
		if m.Type == "" {
			addErr(prefix, fmt.Errorf("type is required"))
			continue
		}
		enabled := true
		if m.Enabled != nil {
			enabled = *m.Enabled
		}
		timeoutSec := m.TimeoutSeconds
		if timeoutSec <= 0 {
			timeoutSec = 10
		}
		mon := &domain.Monitor{
			Name:               m.Name,
			Type:               domain.MonitorType(m.Type),
			Config:             m.Config,
			IntervalSeconds:    m.IntervalSeconds,
			TimeoutSeconds:     timeoutSec,
			Description:        m.Description,
			Enabled:            enabled,
			EscalationPolicyID: m.EscalationPolicyID,
			GroupID:            m.GroupID,
			AutoResolve:        true,
			Labels:             m.Labels,
		}
		if err := prepareNewMonitor(ctx, h.store, org, mon, now); err != nil {
			addErr(prefix, err)
			continue
		}
		monitors = append(monitors, mon)
	}

	if len(errs) > 0 {
		status := http.StatusBadRequest
		if planRejected {
			status = http.StatusForbidden
		}
		jsonResponse(w, status, importSummary{
			Created:         map[string]int{},
			Errors:          errs,
			Error:           fmt.Sprintf("Import rejected, nothing was created. %s", errs[0]),
			UpgradeRequired: upgrade,
		})
		return
	}

	summary := importSummary{
		Created: map[string]int{
			"notification_channels": 0,
			"teams":                 0,
			"escalation_policies":   0,
			"monitors":              0,
		},
		Errors: []string{},
	}

	// Create in dependency order: channels -> teams -> policies -> monitors.

	// 1. Notification Channels. Each item passes the same type, plan,
	// limit, config and URL checks as a single create, and a failure is
	// reported per item so the rest of the import still lands.
	for i, ch := range req.NotificationChannels {
		label := fmt.Sprintf("notification_channels[%d] %q", i, ch.Name)
		if msg := retiredChannelType(ch.Type); msg != "" {
			summary.Errors = append(summary.Errors, label+": "+msg)
			continue
		}
		if !knownChannelType(ch.Type) {
			summary.Errors = append(summary.Errors, label+": type is not a channel type this server supports")
			continue
		}
		if msg := channelPlanGateReason(r.Context(), h.store, ch.Type); msg != "" {
			summary.Errors = append(summary.Errors, label+": "+msg)
			continue
		}
		if err := checkChannelLimit(r.Context(), h.store, claims.OrgID); err != nil {
			summary.Errors = append(summary.Errors, label+": "+err.Error())
			continue
		}
		configStr := ""
		if ch.Config != nil {
			configStr = string(ch.Config)
		}
		config, err := h.channels.prepareChannelConfig(r.Context(), claims.OrgID, ch.Type, json.RawMessage(sanitizeImportedWebhookConfig(ch.Type, configStr)))
		if err != nil {
			summary.Errors = append(summary.Errors, label+": "+err.Error())
			continue
		}
		enabled := true
		if ch.Enabled != nil {
			enabled = *ch.Enabled
		}
		nc := &domain.NotificationChannel{
			ID:      uuid.New().String(),
			OrgID:   claims.OrgID,
			Name:    ch.Name,
			Type:    ch.Type,
			Config:  string(config),
			Enabled: enabled,
		}
		if err := h.store.NotificationChannels().Create(r.Context(), nc); err != nil {
			summary.Errors = append(summary.Errors, label+": "+err.Error())
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
		if err := h.store.Teams().Create(ctx, team); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("teams[%d] %q: failed to create", i, t.Name))
			continue
		}
		summary.Created["teams"]++
	}

	// 3. Escalation Policies.
	for i, ep := range req.EscalationPolicies {
		policy := &domain.EscalationPolicy{
			ID:          uuid.New().String(),
			OrgID:       claims.OrgID,
			Name:        ep.Name,
			Description: ep.Description,
		}
		if err := h.store.EscalationPolicies().Create(ctx, policy); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("escalation_policies[%d] %q: failed to create", i, ep.Name))
			continue
		}
		summary.Created["escalation_policies"]++
	}

	// 4. Monitors, already validated and prepared above.
	for i, mon := range monitors {
		if err := h.store.Monitors().Create(ctx, mon); err != nil {
			summary.Errors = append(summary.Errors, fmt.Sprintf("monitors[%d] %q: failed to create", i, mon.Name))
			continue
		}
		if len(mon.Labels) > 0 {
			_ = h.store.Monitors().SetLabels(ctx, mon.ID, mon.Labels)
		}
		if h.onChange != nil {
			h.onChange(MonitorCreated, mon.ID)
		}
		summary.Created["monitors"]++
	}

	jsonResponse(w, http.StatusOK, summary)
}

// importTopLevelHints maps keys that look like import sections but are not
// accepted to the key the API reads. Older docs used the short names, and
// before this check an import using them returned 200 with nothing created.
var importTopLevelHints = map[string]string{
	"channels":             "notification_channels",
	"notification_channel": "notification_channels",
	"policies":             "escalation_policies",
	"escalations":          "escalation_policies",
	"escalation_policy":    "escalation_policies",
	"monitor":              "monitors",
	"team":                 "teams",
}

// importMonitorHints does the same for monitor fields.
var importMonitorHints = map[string]string{
	"url":      "config.url",
	"interval": "interval_seconds",
	"timeout":  "timeout_seconds",
	"policy":   "escalation_policy_id",
	"group":    "group_id",
}

var importTopLevelKeys = map[string]bool{
	"monitors": true, "teams": true, "escalation_policies": true, "notification_channels": true,
}

// importShapeErrors rejects unknown top-level sections and monitor fields
// with a known replacement, so a file in the wrong format fails with a
// message naming the right key instead of silently importing nothing.
// Other unknown fields are ignored so exported or annotated files still work.
func importShapeErrors(raw json.RawMessage) []string {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return []string{"the import must be a JSON object"}
	}
	var errs []string
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if importTopLevelKeys[k] {
			continue
		}
		if want, ok := importTopLevelHints[k]; ok {
			errs = append(errs, fmt.Sprintf("%s: unknown section, use %q", k, want))
		} else {
			errs = append(errs, fmt.Sprintf("%s: unknown section. Accepted sections are monitors, teams, escalation_policies and notification_channels", k))
		}
	}
	var monitors []map[string]json.RawMessage
	if m, ok := top["monitors"]; ok && json.Unmarshal(m, &monitors) == nil {
		for i, mon := range monitors {
			fields := make([]string, 0, len(importMonitorHints))
			for f := range importMonitorHints {
				if _, ok := mon[f]; ok {
					fields = append(fields, f)
				}
			}
			sort.Strings(fields)
			for _, f := range fields {
				errs = append(errs, fmt.Sprintf("monitors[%d]: unknown field %q, use %q", i, f, importMonitorHints[f]))
			}
		}
	}
	return errs
}

// importDecodeHint turns a JSON type error into a message that names the
// field without echoing Go type names.
func importDecodeHint(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) && te.Field != "" {
		return fmt.Sprintf("%s has the wrong type", te.Field)
	}
	return "check that every section is a list of objects"
}
