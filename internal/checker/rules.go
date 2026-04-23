package checker

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// EvaluateMatchRules checks the HTTP response against ordered match rules.
// Rules are evaluated in order; the first matching rule wins.
// Returns the matched rule's StateID and HealthClass, or empty strings if no rule matches.
func EvaluateMatchRules(rules []domain.MonitorMatchRule, statusCode int, body string, headers http.Header) (stateID, healthClass string) {
	for _, rule := range rules {
		if matchesRule(rule, statusCode, body, headers) {
			return rule.StateID, rule.HealthClass
		}
	}
	return "", ""
}

// HealthClassToStatus maps a state's health class to the corresponding CheckStatus.
func HealthClassToStatus(healthClass string) domain.CheckStatus {
	switch healthClass {
	case "healthy":
		return domain.StatusUp
	case "degraded":
		return domain.StatusDegraded
	case "unhealthy":
		return domain.StatusDown
	default:
		return domain.StatusDown
	}
}

func matchesRule(rule domain.MonitorMatchRule, statusCode int, body string, headers http.Header) bool {
	// Status code check.
	if rule.StatusCode != nil && *rule.StatusCode != statusCode {
		return false
	}
	if rule.StatusCodeMin != nil && statusCode < *rule.StatusCodeMin {
		return false
	}
	if rule.StatusCodeMax != nil && statusCode > *rule.StatusCodeMax {
		return false
	}

	// Body match check.
	if rule.BodyMatch != "" {
		switch rule.BodyMatchMode {
		case "contains":
			if !strings.Contains(body, rule.BodyMatch) {
				return false
			}
		case "not_contains":
			if strings.Contains(body, rule.BodyMatch) {
				return false
			}
		case "regex":
			matched, err := regexp.MatchString(rule.BodyMatch, body)
			if err != nil || !matched {
				return false
			}
		default: // default to contains
			if !strings.Contains(body, rule.BodyMatch) {
				return false
			}
		}
	}

	// Header match check.
	if rule.HeaderMatch != "" {
		headerVal := headers.Get(rule.HeaderMatch)
		if rule.HeaderValue != "" && headerVal != rule.HeaderValue {
			return false
		}
		if rule.HeaderValue == "" && headerVal == "" {
			return false // header must exist
		}
	}

	return true // all conditions matched
}
