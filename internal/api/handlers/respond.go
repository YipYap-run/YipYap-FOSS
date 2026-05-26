package handlers

import (
	"encoding/json"
	"net/http"
	"reflect"
)

func jsonResponse(w http.ResponseWriter, status int, v interface{}) {
	if v == nil {
		v = []struct{}{}
	} else {
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			v = []struct{}{}
		} else if rv.Kind() == reflect.Ptr && rv.IsNil() {
			v = []struct{}{}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errorResponse(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}

func decodeBody(r *http.Request, v interface{}) error {
	defer func() { _ = r.Body.Close() }()
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20) // 1MB limit
	return json.NewDecoder(r.Body).Decode(v)
}
