package handlers

import "encoding/json"

func isPhoneChannel(_ string) bool                              { return false }
func validatePhoneIfNeeded(_ string, _ json.RawMessage) error   { return nil }
