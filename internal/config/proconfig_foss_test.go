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

// Transactional email (password resets, invites, contact method
// verification) needs the YIPYAP_SMTP_* settings in FOSS too.
func TestLoadSMTPConfig(t *testing.T) {
	t.Setenv("YIPYAP_SMTP_HOST", "smtp.example.com")
	t.Setenv("YIPYAP_SMTP_PORT", "465")
	t.Setenv("YIPYAP_SMTP_USER", "mailer")
	t.Setenv("YIPYAP_SMTP_PASS", "s3cret")
	t.Setenv("YIPYAP_SMTP_FROM", "alerts@example.com")
	c := Load()
	if c.SMTPHost != "smtp.example.com" || c.SMTPPort != 465 || c.SMTPUser != "mailer" || c.SMTPPass != "s3cret" || c.SMTPFrom != "alerts@example.com" {
		t.Fatalf("smtp = %q:%d user %q from %q (pass set %v)", c.SMTPHost, c.SMTPPort, c.SMTPUser, c.SMTPFrom, c.SMTPPass != "")
	}

	for _, k := range []string{"YIPYAP_SMTP_HOST", "YIPYAP_SMTP_PORT", "YIPYAP_SMTP_USER", "YIPYAP_SMTP_PASS", "YIPYAP_SMTP_FROM"} {
		t.Setenv(k, "")
	}
	c = Load()
	if c.SMTPHost != "" || c.SMTPPort != 587 || c.SMTPFrom != "" {
		t.Fatalf("unset smtp = %q:%d from %q, want empty host, port 587, empty from", c.SMTPHost, c.SMTPPort, c.SMTPFrom)
	}
}
