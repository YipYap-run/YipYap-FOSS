package emailcfg

import (
	"errors"
	"strings"
	"testing"
)

const fullSMTP = `{"mode":"smtp","host":"smtp.org.example","port":587,"tls_mode":"starttls","username":"u","password":"p","from":"alerts@org.example","to":"ops@org.example"}`

func fieldOf(t *testing.T, err error) string {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error %v is not a *FieldError", err)
	}
	return fe.Field
}

func TestValidate_PlatformRecipientsOnly(t *testing.T) {
	cfg, err := Validate([]byte(`{"to":"a@x.example; B <b@x.example>, a@x.example"}`), true)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if cfg.Mode != ModePlatform || cfg.To != "a@x.example, b@x.example" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if got := string(cfg.Marshal()); got != `{"mode":"platform","to":"a@x.example, b@x.example"}` {
		t.Fatalf("Marshal = %s", got)
	}
}

func TestValidate_PlatformAcceptsList(t *testing.T) {
	cfg, err := Validate([]byte(`{"mode":"platform","to":["a@x.example","b@x.example"]}`), true)
	if err != nil || cfg.To != "a@x.example, b@x.example" {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestValidate_PlatformRejectsServerKeys(t *testing.T) {
	for _, k := range []string{"host", "port", "tls_mode", "username", "password", "from"} {
		body := `{"mode":"platform","to":"a@x.example","` + k + `":"v"}`
		if k == "port" {
			body = `{"mode":"platform","to":"a@x.example","port":25}`
		}
		_, err := Validate([]byte(body), true)
		if err == nil || fieldOf(t, err) != k {
			t.Errorf("%s: err = %v, want a %s field error", k, err, k)
		}
	}
}

func TestValidate_PlatformIgnoresBlankServerKeys(t *testing.T) {
	// Older UI builds post every field, blank ones included.
	_, err := Validate([]byte(`{"host":"","port":0,"username":"","password":"","from":"","to":"a@x.example"}`), true)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestValidate_PlatformUnavailable(t *testing.T) {
	_, err := Validate([]byte(`{"to":"a@x.example"}`), false)
	if !errors.Is(err, ErrPlatformUnavailable) {
		t.Fatalf("err = %v, want ErrPlatformUnavailable", err)
	}
	if _, err := Validate([]byte(fullSMTP), false); err != nil {
		t.Fatalf("smtp mode must work without a platform mailer: %v", err)
	}
}

func TestValidate_Recipients(t *testing.T) {
	cases := map[string]string{
		"missing":   `{}`,
		"blank":     `{"to":"  "}`,
		"invalid":   `{"to":"not-an-address"}`,
		"header":    `{"to":"a@x.example\r\nBcc: e@evil.example"}`,
		"too many":  `{"to":"` + manyAddrs(11) + `"}`,
		"bad shape": `{"to":5}`,
	}
	for name, body := range cases {
		_, err := Validate([]byte(body), true)
		if err == nil || fieldOf(t, err) != "to" {
			t.Errorf("%s: err = %v, want a to field error", name, err)
		}
	}
	if _, err := Validate([]byte(`{"to":"`+manyAddrs(10)+`"}`), true); err != nil {
		t.Fatalf("10 recipients: %v", err)
	}
}

func manyAddrs(n int) string {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, "u"+string(rune('a'+i))+"@x.example")
	}
	return strings.Join(parts, ",")
}

func TestValidate_SMTPRequiresEveryField(t *testing.T) {
	for _, k := range []string{"host", "port", "tls_mode", "username", "password", "from"} {
		body := strings.Replace(fullSMTP, `"`+k+`":`, `"x_`+k+`":`, 1)
		_, err := Validate([]byte(body), true)
		if err == nil || fieldOf(t, err) != k {
			t.Errorf("missing %s: err = %v, want a %s field error", k, err, k)
		}
	}
	if _, err := Validate([]byte(fullSMTP), true); err != nil {
		t.Fatalf("full config: %v", err)
	}
}

func TestValidate_SMTPFieldFormats(t *testing.T) {
	cases := map[string]struct{ from, to, field string }{
		"scheme in host": {`"host":"smtp.org.example"`, `"host":"smtp://smtp.org.example"`, "host"},
		"port in host":   {`"host":"smtp.org.example"`, `"host":"smtp.org.example:25"`, "host"},
		"port range":     {`"port":587`, `"port":70000`, "port"},
		"tls none":       {`"tls_mode":"starttls"`, `"tls_mode":"none"`, "tls_mode"},
		"bad from":       {`"from":"alerts@org.example"`, `"from":"alerts"`, "from"},
		"bad mode":       {`"mode":"smtp"`, `"mode":"relay"`, "mode"},
	}
	for name, c := range cases {
		body := strings.Replace(fullSMTP, c.from, c.to, 1)
		_, err := Validate([]byte(body), true)
		if err == nil || fieldOf(t, err) != c.field {
			t.Errorf("%s: err = %v, want a %s field error", name, err, c.field)
		}
	}
}

func TestValidate_LegacyHostMeansSMTP(t *testing.T) {
	// A legacy row with a host is smtp mode, so it now needs every field.
	_, err := Validate([]byte(`{"host":"smtp.org.example","port":587,"to":"a@x.example"}`), true)
	if err == nil || fieldOf(t, err) != "tls_mode" {
		t.Fatalf("err = %v, want tls_mode field error", err)
	}
}

func TestValidate_PortAsString(t *testing.T) {
	body := strings.Replace(fullSMTP, `"port":587`, `"port":"587"`, 1)
	cfg, err := Validate([]byte(body), true)
	if err != nil || cfg.Port != 587 {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
}

func TestResolve_PlatformDropsServerSettings(t *testing.T) {
	cfg, err := Resolve([]byte(`{"mode":"platform","host":"evil.example","username":"u","password":"p","to":"a@x.example"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg != (Config{Mode: ModePlatform, To: "a@x.example"}) {
		t.Fatalf("cfg = %+v", cfg)
	}
}
