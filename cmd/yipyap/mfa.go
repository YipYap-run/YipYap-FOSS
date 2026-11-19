package main

// setupMFA is a no-op in FOSS builds, which ship no MFA store.
func setupMFA(_ []byte) {}
