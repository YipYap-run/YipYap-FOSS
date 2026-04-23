package handlers

import "net/http"

// Status is a stub  - status pages are not available in FOSS builds.
func (h *PublicHandler) Status(w http.ResponseWriter, r *http.Request) {
	errorResponse(w, http.StatusNotFound, "status pages not available")
}
