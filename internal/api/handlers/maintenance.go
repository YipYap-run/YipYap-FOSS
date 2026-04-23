package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
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

type MaintenanceHandler struct {
	store store.Store
}

func NewMaintenanceHandler(s store.Store) *MaintenanceHandler {
	return &MaintenanceHandler{store: s}
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
	var mw domain.MaintenanceWindow
	if err := decodeBody(r, &mw); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	mw.ID = uuid.New().String()
	mw.OrgID = claims.OrgID
	mw.CreatedBy = claims.UserID
	mw.CreatedAt = now
	if mw.RecurrenceType == "" {
		mw.RecurrenceType = "none"
	}
	if mw.DaysOfWeek == "" {
		mw.DaysOfWeek = "[]"
	}
	if err := validateRecurrenceFields(mw.RecurrenceType, mw.DaysOfWeek, mw.DayOfMonth); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.MaintenanceWindows().Create(r.Context(), &mw); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create maintenance window")
		return
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

	var req struct {
		Name            *string    `json:"name"`
		Description     *string    `json:"description"`
		StartAt         *time.Time `json:"start_at"`
		EndAt           *time.Time `json:"end_at"`
		Public          *bool      `json:"public"`
		SuppressAlerts  *bool      `json:"suppress_alerts"`
		RecurrenceType  *string    `json:"recurrence_type"`
		RecurrenceEndAt *string    `json:"recurrence_end_at"`
		DaysOfWeek      *string    `json:"days_of_week"`
		DayOfMonth      *int       `json:"day_of_month"`
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
	if req.StartAt != nil {
		mw.StartAt = *req.StartAt
	}
	if req.EndAt != nil {
		mw.EndAt = *req.EndAt
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
	if req.RecurrenceEndAt != nil {
		if *req.RecurrenceEndAt == "" {
			mw.RecurrenceEndAt = nil
		} else {
			t, err := time.Parse(time.RFC3339, *req.RecurrenceEndAt)
			if err != nil {
				errorResponse(w, http.StatusBadRequest, "invalid recurrence_end_at format")
				return
			}
			mw.RecurrenceEndAt = &t
		}
	}
	if req.DaysOfWeek != nil {
		mw.DaysOfWeek = *req.DaysOfWeek
	}
	if req.DayOfMonth != nil {
		mw.DayOfMonth = *req.DayOfMonth
	}
	if err := validateRecurrenceFields(mw.RecurrenceType, mw.DaysOfWeek, mw.DayOfMonth); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.store.MaintenanceWindows().Update(r.Context(), mw); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update maintenance window")
		return
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
	if err := h.store.MaintenanceWindows().Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusNotFound, "maintenance window not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
