package handlers

import (
	"net/http"
	"strconv"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func paginationFromQuery(r *http.Request) store.ListParams {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return store.ListParams{
		Limit:  limit,
		Offset: offset,
	}
}
