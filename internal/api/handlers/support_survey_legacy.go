package handlers

import "net/http"

func LegacySurveyRedirect(*http.Request) (string, bool) {
	return "", false
}
