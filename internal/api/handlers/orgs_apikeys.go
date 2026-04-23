package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func (h *OrgHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	keys, err := h.store.APIKeys().ListByOrg(r.Context(), claims.OrgID, params)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list api keys")
		return
	}
	jsonResponse(w, http.StatusOK, keys)
}

func (h *OrgHandler) CreateAPIKey(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())

	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		errorResponse(w, http.StatusBadRequest, "name is required")
		return
	}

	if h.hasher == nil {
		errorResponse(w, http.StatusInternalServerError, "api key generation not configured")
		return
	}
	plaintext, hash, prefix, genErr := h.hasher.GenerateAPIKey()
	if genErr != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to generate api key")
		return
	}

	now := time.Now().UTC().Truncate(time.Second)
	apiKey := &domain.APIKey{
		ID:        uuid.New().String(),
		OrgID:     claims.OrgID,
		Name:      req.Name,
		KeyHash:   hash,
		Prefix:    prefix,
		Scopes:    req.Scopes,
		CreatedBy: claims.UserID,
		CreatedAt: now,
	}

	if err := h.store.APIKeys().Create(r.Context(), apiKey); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create api key")
		return
	}

	jsonResponse(w, http.StatusCreated, map[string]interface{}{
		"key":     plaintext,
		"id":      apiKey.ID,
		"name":    apiKey.Name,
		"prefix":  apiKey.Prefix,
		"scopes":  apiKey.Scopes,
		"created": apiKey.CreatedAt,
	})
}

func (h *OrgHandler) DeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	key, err := h.store.APIKeys().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "api key not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if key.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "api key not found")
		return
	}
	if err := h.store.APIKeys().Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusNotFound, "api key not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
