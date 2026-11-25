package limits

import (
	"strings"
	"testing"
)

func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestFromEnvDefaultsUnlimited(t *testing.T) {
	c, err := FromEnv(envMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c != Default() {
		t.Fatalf("got %+v, want %+v", c, Default())
	}
	if c.MaxMonitors != 0 || c.MaxNotificationChannels != 0 || c.MaxEscalationPolicies != 0 || c.MaxMembers != 0 {
		t.Fatalf("caps should default to unlimited, got %+v", c)
	}
	if c.MinCheckIntervalSeconds != DefaultMinCheckIntervalSeconds {
		t.Fatalf("interval floor = %d", c.MinCheckIntervalSeconds)
	}
}

func TestFromEnvParses(t *testing.T) {
	c, err := FromEnv(envMap(map[string]string{
		EnvMaxMonitors:             " 2 ",
		EnvMaxNotificationChannels: "0",
		EnvMaxEscalationPolicies:   "3",
		EnvMaxMembers:              "50",
		EnvMinCheckInterval:        "30",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Caps{MaxMonitors: 2, MaxEscalationPolicies: 3, MaxMembers: 50, MinCheckIntervalSeconds: 30}
	if c != want {
		t.Fatalf("got %+v, want %+v", c, want)
	}
}

func TestFromEnvRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		EnvMaxMonitors:             "-1",
		EnvMaxNotificationChannels: "five",
		EnvMaxEscalationPolicies:   "1.5",
		EnvMaxMembers:              "99999999999",
		EnvMinCheckInterval:        "0",
	}
	for env, val := range cases {
		_, err := FromEnv(envMap(map[string]string{env: val}))
		if err == nil {
			t.Errorf("%s=%q: expected an error", env, val)
			continue
		}
		if !strings.Contains(err.Error(), env) {
			t.Errorf("%s=%q: error %q does not name the variable", env, val, err)
		}
	}
}

func TestCapErrorWording(t *testing.T) {
	msg := (&CapError{Resource: "monitors", Max: 2, Env: EnvMaxMonitors}).Error()
	if strings.Contains(strings.ToLower(msg), "upgrade") || strings.Contains(strings.ToLower(msg), "plan") {
		t.Fatalf("operator cap message must not mention plans or upgrades: %q", msg)
	}
	if !strings.Contains(msg, EnvMaxMonitors) || !strings.Contains(msg, "2 monitors") {
		t.Fatalf("unexpected message: %q", msg)
	}
}

func TestSetCurrent(t *testing.T) {
	t.Cleanup(func() { Set(Default()) })
	Set(Caps{MaxMonitors: 7, MinCheckIntervalSeconds: 60})
	if got := Current().MaxMonitors; got != 7 {
		t.Fatalf("Current().MaxMonitors = %d", got)
	}
}
