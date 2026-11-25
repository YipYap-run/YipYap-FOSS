package handlers

// proofFields is empty in FOSS builds, which support no credential assertions
// for step-up.
type proofFields struct{}

func (mfaProof) hasAssertion() bool { return false }
