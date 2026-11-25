package config

import "testing"

func TestLoadOperatorCaps(t *testing.T) {
	t.Setenv("YIPYAP_MAX_MONITORS_PER_ORG", "")
	t.Setenv("YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG", "")
	t.Setenv("YIPYAP_MIN_CHECK_INTERVAL_SECONDS", "")
	c := Load()
	if c.MaxMonitorsPerOrg != 0 || c.MaxNotificationChannelsPerOrg != 0 || c.MinCheckIntervalSeconds != 0 {
		t.Fatalf("unset caps = %d/%d/%d, want 0 (no cap)", c.MaxMonitorsPerOrg, c.MaxNotificationChannelsPerOrg, c.MinCheckIntervalSeconds)
	}

	t.Setenv("YIPYAP_MAX_MONITORS_PER_ORG", "25")
	t.Setenv("YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG", "-3")
	t.Setenv("YIPYAP_MIN_CHECK_INTERVAL_SECONDS", "fast")
	c = Load()
	if c.MaxMonitorsPerOrg != 25 {
		t.Errorf("MaxMonitorsPerOrg = %d, want 25", c.MaxMonitorsPerOrg)
	}
	if c.MaxNotificationChannelsPerOrg != 0 || c.MinCheckIntervalSeconds != 0 {
		t.Errorf("bad values = %d/%d, want ignored (0)", c.MaxNotificationChannelsPerOrg, c.MinCheckIntervalSeconds)
	}
}
