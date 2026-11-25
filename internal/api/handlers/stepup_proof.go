package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

// mfaProof is the second-factor proof a sensitive account change carries.
// Every step-up endpoint takes the same fields, so the type is shared. The
// concrete proof fields come from the edition: an authenticator or backup
// code always, plus a credential assertion where that edition supports one.
type mfaProof struct {
	// MFACode is a current one-time code or an unused backup code.
	MFACode string `json:"mfa_code"`
	proofFields
}

func (p mfaProof) present() bool {
	return p.MFACode != "" || p.hasAssertion()
}

// MFAProofRequiredCode is the machine-readable code returned when a
// step-up request is missing the second-factor proof.
const MFAProofRequiredCode = "mfa_proof_required"

// MFAProofInvalidCode is returned when the proof does not verify.
const MFAProofInvalidCode = "mfa_proof_invalid"

const MFALockedCode = "mfa_locked"

type mfaLockedError struct {
	Until time.Time
}

func (e *mfaLockedError) Error() string {
	return "second factor locked until " + e.Until.Format(time.RFC3339)
}

func retryAfterSeconds(until time.Time) int {
	seconds := int(time.Until(until).Seconds()) + 1
	if seconds < 1 {
		return 1
	}
	return seconds
}

func writeMFALocked(w http.ResponseWriter, locked *mfaLockedError) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(locked.Until)))
	writeCodedError(w, http.StatusTooManyRequests, MFALockedCode,
		"too many failed attempts; try again later")
}

var (
	errProofMissing = errors.New("missing second-factor proof")
	errProofInvalid = errors.New("invalid second-factor proof")
)

// writeStepUpError maps a verifyMFAIfEnabled error to a response. A missing
// or wrong proof gets status (the caller's existing status for the
// endpoint) with a code the SPA uses to show the right prompt; anything
// else is a server error.
func writeStepUpError(w http.ResponseWriter, status int, err error) {
	var locked *mfaLockedError
	if errors.As(err, &locked) {
		writeMFALocked(w, locked)
		return
	}
	switch {
	case errors.Is(err, errProofMissing):
		writeCodedError(w, status, MFAProofRequiredCode,
			"enter a code from your authenticator app, a backup code, or use a security key")
	case errors.Is(err, errProofInvalid):
		writeCodedError(w, status, MFAProofInvalidCode, "invalid verification code or key")
	default:
		errorResponse(w, http.StatusInternalServerError, "failed to verify second factor")
	}
}

func writeCodedError(w http.ResponseWriter, status int, code, message string) {
	jsonResponse(w, status, map[string]string{"error": message, "code": code})
}
