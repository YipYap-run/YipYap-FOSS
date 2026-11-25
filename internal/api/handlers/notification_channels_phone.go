package handlers

import "encoding/json"

func isPhoneChannel(_ string) bool { return false }

func phoneResponseConfig(_, config string) string { return config }

func retiredChannelType(_ string) string { return "" }

func validatePhoneIfNeeded(_ string, config json.RawMessage) (json.RawMessage, error) {
	return config, nil
}
