package handlers

import "net/http"

// StatusPage is a no-op in FOSS builds; status pages are a paid feature.
func (h *PublicHandler) StatusPage(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}
