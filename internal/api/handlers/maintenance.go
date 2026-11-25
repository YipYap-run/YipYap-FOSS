package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

var validRecurrenceTypes = map[string]bool{
	"none": true, "daily": true, "weekly": true, "monthly": true,
}

func validateRecurrenceFields(recType, daysOfWeek string, dayOfMonth int) error {
	if recType != "" && !validRecurrenceTypes[recType] {
		return fmt.Errorf("recurrence_type must be one of: none, daily, weekly, monthly")
	}
	if recType == "weekly" {
		var days []int
		if err := json.Unmarshal([]byte(daysOfWeek), &days); err != nil || len(days) == 0 {
			return fmt.Errorf("days_of_week must be a JSON array of integers 0-6 for weekly recurrence")
		}
		for _, d := range days {
			if d < 0 || d > 6 {
				return fmt.Errorf("days_of_week values must be 0-6 (Sun-Sat)")
			}
		}
	}
	if recType == "monthly" {
		if dayOfMonth < 1 || dayOfMonth > 31 {
			return fmt.Errorf("day_of_month must be 1-31 for monthly recurrence")
		}
	}
	return nil
}

// parseRecurrenceEnd parses an optional recurrence_end_at value. An empty
// string clears it.
func parseRecurrenceEnd(v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("invalid recurrence_end_at format")
	}
	return &t, nil
}

// windowTimes carries the time fields a create or update request may set.
// The SPA sends wall-clock values (start_local, end_local,
// recurrence_end_local) plus timezone and the server resolves them in that
// zone. API clients may still send absolute RFC3339 start_at / end_at /
// recurrence_end_at. A local value wins over its absolute counterpart.
type windowTimes struct {
	Timezone           *string    `json:"timezone"`
	StartAt            *time.Time `json:"start_at"`
	EndAt              *time.Time `json:"end_at"`
	RecurrenceEndAt    *string    `json:"recurrence_end_at"`
	StartLocal         *string    `json:"start_local"`
	EndLocal           *string    `json:"end_local"`
	RecurrenceEndLocal *string    `json:"recurrence_end_local"`
}

// apply resolves the request's times onto mw. Fields the request leaves out
// keep their current values. Changing only the timezone keeps the stored
// instants; send the local values too to keep the wall-clock times.
func (wt *windowTimes) apply(mw *domain.MaintenanceWindow) error {
	if wt.Timezone != nil {
		loc, err := domain.LoadMaintenanceLocation(*wt.Timezone)
		if err != nil {
			return err
		}
		mw.Timezone = loc.String()
	}
	if mw.Timezone == "" {
		mw.Timezone = domain.DefaultMaintenanceTimezone
	}
	loc := mw.Location()

	if wt.StartLocal != nil && *wt.StartLocal != "" {
		t, err := domain.ParseMaintenanceLocal(*wt.StartLocal, loc)
		if err != nil {
			return fmt.Errorf("start_local %v", err)
		}
		mw.StartAt = t
	} else if wt.StartAt != nil {
		mw.StartAt = wt.StartAt.UTC()
	}
	if wt.EndLocal != nil && *wt.EndLocal != "" {
		t, err := domain.ParseMaintenanceLocal(*wt.EndLocal, loc)
		if err != nil {
			return fmt.Errorf("end_local %v", err)
		}
		mw.EndAt = t
	} else if wt.EndAt != nil {
		mw.EndAt = wt.EndAt.UTC()
	}
	switch {
	case wt.RecurrenceEndLocal != nil:
		if *wt.RecurrenceEndLocal == "" {
			mw.RecurrenceEndAt = nil
			break
		}
		t, err := domain.ParseMaintenanceLocal(*wt.RecurrenceEndLocal, loc)
		if err != nil {
			return fmt.Errorf("recurrence_end_local %v", err)
		}
		mw.RecurrenceEndAt = &t
	case wt.RecurrenceEndAt != nil:
		t, err := parseRecurrenceEnd(*wt.RecurrenceEndAt)
		if err != nil {
			return err
		}
		mw.RecurrenceEndAt = t
	}
	return nil
}

// Length limits match the settings form's maxlength attributes. They are
// counted in characters so non-Latin names get the same room.
const (
	maxMaintenanceNameLen        = 200
	maxMaintenanceDescriptionLen = 2000
)

// validateWindow checks the fields shared by create and update.
func validateWindow(mw *domain.MaintenanceWindow) error {
	if strings.TrimSpace(mw.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if utf8.RuneCountInString(mw.Name) > maxMaintenanceNameLen {
		return fmt.Errorf("name must be %d characters or fewer", maxMaintenanceNameLen)
	}
	if utf8.RuneCountInString(mw.Description) > maxMaintenanceDescriptionLen {
		return fmt.Errorf("description must be %d characters or fewer", maxMaintenanceDescriptionLen)
	}
	if mw.StartAt.IsZero() || mw.EndAt.IsZero() {
		return fmt.Errorf("start_at and end_at are required")
	}
	if !mw.EndAt.After(mw.StartAt) {
		return fmt.Errorf("end_at must be after start_at")
	}
	if mw.RecurrenceEndAt != nil && mw.RecurrenceEndAt.Before(mw.StartAt) {
		return fmt.Errorf("recurrence_end_at must not be before start_at")
	}
	return validateRecurrenceFields(mw.RecurrenceType, mw.DaysOfWeek, mw.DayOfMonth)
}

type MaintenanceHandler struct {
	store   store.Store
	emitter cloudevents.Emitter
}

func NewMaintenanceHandler(s store.Store) *MaintenanceHandler {
	return &MaintenanceHandler{store: s}
}

// NewMaintenanceHandlerWithEmitter builds a MaintenanceHandler that emits
// maintenance.started.v1 / maintenance.ended.v1 CloudEvents on synchronous
// lifecycle transitions driven by API requests (create, update, delete).
// Scheduled transitions (start_at/end_at reached without a user action) are
// handled by the Ticker in internal/maintenance, not here.
//
// Pass nil for em to disable emission.
func NewMaintenanceHandlerWithEmitter(s store.Store, em cloudevents.Emitter) *MaintenanceHandler {
	return &MaintenanceHandler{store: s, emitter: em}
}

// monitorsList returns the monitors slice for a started-event payload.
// Convention: empty slice means the window is org-wide (no MonitorID set);
// a single-monitor window returns []string{monitorID}.
func monitorsList(mw *domain.MaintenanceWindow) []string {
	if mw.MonitorID == "" {
		return []string{}
	}
	return []string{mw.MonitorID}
}

// scheduledEnd returns the single scheduled end time for a one-shot window,
// or the zero time for recurring windows (they don't have one fixed end).
func scheduledEnd(mw *domain.MaintenanceWindow) time.Time {
	if mw.RecurrenceType == "" || mw.RecurrenceType == "none" {
		return mw.EndAt
	}
	return time.Time{}
}

// monitorInOrg reports whether monitorID is empty (an org-wide window) or
// names a monitor owned by orgID. Callers return 404 otherwise so a foreign
// monitor id is indistinguishable from a missing one.
func (h *MaintenanceHandler) monitorInOrg(r *http.Request, monitorID, orgID string) bool {
	if monitorID == "" {
		return true
	}
	m, err := h.store.Monitors().GetByID(r.Context(), monitorID)
	return err == nil && m != nil && m.OrgID == orgID
}

func (h *MaintenanceHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	windows, err := h.store.MaintenanceWindows().ListByOrg(r.Context(), claims.OrgID, params)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list maintenance windows")
		return
	}
	jsonResponse(w, http.StatusOK, windows)
}

func (h *MaintenanceHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mw, err := h.store.MaintenanceWindows().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "maintenance window not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if mw.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "maintenance window not found")
		return
	}
	jsonResponse(w, http.StatusOK, mw)
}

func (h *MaintenanceHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	// Decode into a request struct rather than domain.MaintenanceWindow so
	// recurrence_end_at can be "" or null (time.Time rejects ""), matching
	// Update.
	var req struct {
		windowTimes
		Name           string `json:"name"`
		Description    string `json:"description"`
		MonitorID      string `json:"monitor_id"`
		Public         bool   `json:"public"`
		SuppressAlerts *bool  `json:"suppress_alerts"`
		RecurrenceType string `json:"recurrence_type"`
		DaysOfWeek     string `json:"days_of_week"`
		DayOfMonth     int    `json:"day_of_month"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	mw := domain.MaintenanceWindow{
		ID:             uuid.New().String(),
		OrgID:          claims.OrgID,
		MonitorID:      req.MonitorID,
		Name:           strings.TrimSpace(req.Name),
		Description:    req.Description,
		Public:         req.Public,
		SuppressAlerts: true,
		RecurrenceType: req.RecurrenceType,
		DaysOfWeek:     req.DaysOfWeek,
		DayOfMonth:     req.DayOfMonth,
		CreatedAt:      now,
		CreatedBy:      claims.UserID,
	}
	if req.SuppressAlerts != nil {
		mw.SuppressAlerts = *req.SuppressAlerts
	}
	if err := req.apply(&mw); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	if mw.RecurrenceType == "" {
		mw.RecurrenceType = "none"
	}
	if mw.DaysOfWeek == "" {
		mw.DaysOfWeek = "[]"
	}
	if err := validateWindow(&mw); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.monitorInOrg(r, mw.MonitorID, claims.OrgID) {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	if err := h.store.MaintenanceWindows().Create(r.Context(), &mw); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create maintenance window")
		return
	}

	// If the window is already active at the moment of creation (e.g. user
	// creates a "starts right now" window), emit maintenance.started.v1
	// synchronously. Future-dated windows are picked up by the ticker when
	// StartAt is reached.
	if mw.IsActiveAt(now) {
		_ = cloudevents.EmitMaintenanceStarted(r.Context(), h.emitter, mw.OrgID, types.MaintenanceStartedV1Data{
			WindowID:     mw.ID,
			Monitors:     monitorsList(&mw),
			StartedAt:    now,
			ScheduledEnd: scheduledEnd(&mw),
		})
	}

	jsonResponse(w, http.StatusCreated, &mw)
}

func (h *MaintenanceHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	mw, err := h.store.MaintenanceWindows().GetByID(r.Context(), id)
	if err != nil || mw.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "maintenance window not found")
		return
	}

	// Snapshot pre-update state so we can detect active/inactive transitions
	// after the mutation and emit maintenance.started/ended accordingly.
	pre := *mw

	var req struct {
		windowTimes
		Name           *string `json:"name"`
		Description    *string `json:"description"`
		MonitorID      *string `json:"monitor_id"`
		Public         *bool   `json:"public"`
		SuppressAlerts *bool   `json:"suppress_alerts"`
		RecurrenceType *string `json:"recurrence_type"`
		DaysOfWeek     *string `json:"days_of_week"`
		DayOfMonth     *int    `json:"day_of_month"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		mw.Name = *req.Name
	}
	if req.Description != nil {
		mw.Description = *req.Description
	}
	if req.MonitorID != nil {
		mw.MonitorID = *req.MonitorID
	}
	if req.Public != nil {
		mw.Public = *req.Public
	}
	if req.SuppressAlerts != nil {
		mw.SuppressAlerts = *req.SuppressAlerts
	}
	if req.RecurrenceType != nil {
		mw.RecurrenceType = *req.RecurrenceType
	}
	if err := req.apply(mw); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.DaysOfWeek != nil {
		mw.DaysOfWeek = *req.DaysOfWeek
	}
	if req.DayOfMonth != nil {
		mw.DayOfMonth = *req.DayOfMonth
	}
	if err := validateWindow(mw); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.MonitorID != nil && !h.monitorInOrg(r, mw.MonitorID, claims.OrgID) {
		errorResponse(w, http.StatusNotFound, "monitor not found")
		return
	}

	if err := h.store.MaintenanceWindows().Update(r.Context(), mw); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update maintenance window")
		return
	}

	// Detect active-state transitions driven by the update. We compute
	// IsActiveAt(now) on both the pre-image and the updated copy; extending
	// an already-active window (still active) or shifting a still-inactive
	// window's dates without crossing the active boundary emit nothing.
	now := time.Now().UTC()
	prevActive := pre.IsActiveAt(now)
	newActive := mw.IsActiveAt(now)
	switch {
	case !prevActive && newActive:
		_ = cloudevents.EmitMaintenanceStarted(r.Context(), h.emitter, mw.OrgID, types.MaintenanceStartedV1Data{
			WindowID:     mw.ID,
			Monitors:     monitorsList(mw),
			StartedAt:    now,
			ScheduledEnd: scheduledEnd(mw),
		})
	case prevActive && !newActive:
		_ = cloudevents.EmitMaintenanceEnded(r.Context(), h.emitter, mw.OrgID, types.MaintenanceEndedV1Data{
			WindowID:   mw.ID,
			EndedAt:    now,
			EndedEarly: true,
		})
	}

	jsonResponse(w, http.StatusOK, mw)
}

func (h *MaintenanceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	mw, err := h.store.MaintenanceWindows().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "maintenance window not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if mw.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "maintenance window not found")
		return
	}
	// Capture active-state before delete so we can emit an "ended early"
	// event if the user tore down a currently-active window.
	now := time.Now().UTC()
	wasActive := mw.IsActiveAt(now)

	if err := h.store.MaintenanceWindows().Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusNotFound, "maintenance window not found")
		return
	}

	if wasActive {
		_ = cloudevents.EmitMaintenanceEnded(r.Context(), h.emitter, mw.OrgID, types.MaintenanceEndedV1Data{
			WindowID:   mw.ID,
			EndedAt:    now,
			EndedEarly: true,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}
