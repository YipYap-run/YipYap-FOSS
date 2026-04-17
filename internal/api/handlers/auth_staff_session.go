package handlers

import "net/http"

func (h *AuthHandler) AcceptStaffSession(w http.ResponseWriter, r *http.Request) {
	errorResponse(w, http.StatusNotFound, "not found")
}
