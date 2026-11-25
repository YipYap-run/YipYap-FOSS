package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// SettingChannelFailureAlertThreshold is the org setting that holds how
// many deliveries in a row a channel must fail before org owners are
// emailed. The value is a decimal integer.
const SettingChannelFailureAlertThreshold = "channel_failure_alert_threshold"

// Bounds and default for the channel failure alert threshold.
const (
	DefaultChannelFailureAlertThreshold = 3
	MinChannelFailureAlertThreshold     = 1
	MaxChannelFailureAlertThreshold     = 20
)

// ValidateChannelFailureAlertThreshold reports whether n is in range.
func ValidateChannelFailureAlertThreshold(n int) error {
	if n < MinChannelFailureAlertThreshold || n > MaxChannelFailureAlertThreshold {
		return fmt.Errorf("threshold must be between %d and %d", MinChannelFailureAlertThreshold, MaxChannelFailureAlertThreshold)
	}
	return nil
}

// ChannelFailureAlertThresholdFromSettings reads the threshold from an
// org's settings map. A missing or invalid value gives the default.
func ChannelFailureAlertThresholdFromSettings(settings map[string]string) int {
	v, ok := settings[SettingChannelFailureAlertThreshold]
	if !ok {
		return DefaultChannelFailureAlertThreshold
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || ValidateChannelFailureAlertThreshold(n) != nil {
		return DefaultChannelFailureAlertThreshold
	}
	return n
}
