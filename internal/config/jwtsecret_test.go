package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const validSecret = "0123456789abcdef0123456789abcdef0123456789abcdef"

func TestResolveJWTSecret(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		generate   bool
		wantErr    string // substring; empty means success
		wantSrc    JWTSecretSource
	}{
		{name: "valid used as-is", configured: validSecret, generate: true, wantSrc: JWTSecretFromEnv},
		{name: "valid without generation", configured: validSecret, generate: false, wantSrc: JWTSecretFromEnv},
		{name: "compose placeholder", configured: "change-me-in-production", generate: true, wantErr: "placeholder"},
		{name: "readme placeholder", configured: "your-secret-here", generate: true, wantErr: "placeholder"},
		{name: "goreleaser placeholder", configured: "your-secret", generate: true, wantErr: "placeholder"},
		{name: "changeme any case", configured: "  ChangeMe ", generate: true, wantErr: "placeholder"},
		{name: "secret", configured: "secret", generate: false, wantErr: "placeholder"},
		{name: "short", configured: "a-real-but-short-secret", generate: true, wantErr: "at least 32"},
		{name: "31 bytes", configured: strings.Repeat("x", 31), generate: true, wantErr: "at least 32"},
		{name: "unset without generation", configured: "", generate: false, wantErr: "is required"},
		{name: "unset generates", configured: "", generate: true, wantSrc: JWTSecretFromGenerated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			got, src, _, err := ResolveJWTSecret(JWTSecretOptions{Configured: tt.configured, DataDir: dir, AllowGenerate: tt.generate})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				if !strings.Contains(err.Error(), JWTSecretEnv) {
					t.Fatalf("error %q does not name %s", err, JWTSecretEnv)
				}
				if tt.configured != "" && strings.Contains(err.Error(), strings.TrimSpace(tt.configured)) {
					t.Fatalf("error leaks the configured value: %q", err)
				}
				if _, statErr := os.Stat(filepath.Join(dir, JWTSecretFile)); statErr == nil {
					t.Fatal("secret file written on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if src != tt.wantSrc {
				t.Fatalf("source = %v, want %v", src, tt.wantSrc)
			}
			if tt.configured != "" && got != tt.configured {
				t.Fatal("configured secret not used as-is")
			}
			if len(got) < MinJWTSecretLen {
				t.Fatalf("secret is %d bytes", len(got))
			}
			if tt.configured != "" {
				if _, statErr := os.Stat(filepath.Join(dir, JWTSecretFile)); statErr == nil {
					t.Fatal("secret file written although the env var was set")
				}
			}
		})
	}
}

func TestResolveJWTSecretPersists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "data")
	first, src, path, err := ResolveJWTSecret(JWTSecretOptions{DataDir: dir, AllowGenerate: true})
	if err != nil {
		t.Fatal(err)
	}
	if src != JWTSecretFromGenerated {
		t.Fatalf("first source = %v, want generated", src)
	}
	if len(first) != 2*generatedSecretBytes {
		t.Fatalf("generated secret is %d chars, want %d", len(first), 2*generatedSecretBytes)
	}
	if path != filepath.Join(dir, JWTSecretFile) {
		t.Fatalf("path = %q", path)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("secret file mode = %o, want 600", perm)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("data dir has %d entries, want only the secret file (temp file left behind?)", len(entries))
	}

	second, src, _, err := ResolveJWTSecret(JWTSecretOptions{DataDir: dir, AllowGenerate: true})
	if err != nil {
		t.Fatal(err)
	}
	if src != JWTSecretFromFile {
		t.Fatalf("second source = %v, want file", src)
	}
	if second != first {
		t.Fatal("second boot returned a different secret")
	}

	// A set env var still wins over the stored file.
	third, src, _, err := ResolveJWTSecret(JWTSecretOptions{Configured: validSecret, DataDir: dir, AllowGenerate: true})
	if err != nil || src != JWTSecretFromEnv || third != validSecret {
		t.Fatalf("env did not win: src=%v err=%v", src, err)
	}
}

func TestResolveJWTSecretConcurrentBootsAgree(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	var wg sync.WaitGroup
	out := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i], _, _, errs[i] = ResolveJWTSecret(JWTSecretOptions{DataDir: dir, AllowGenerate: true})
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if out[i] != out[0] {
			t.Fatal("concurrent resolvers disagree on the secret")
		}
	}
}

func TestResolveJWTSecretCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, JWTSecretFile)
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := ResolveJWTSecret(JWTSecretOptions{DataDir: dir, AllowGenerate: true})
	if err == nil || !strings.Contains(err.Error(), JWTSecretEnv) {
		t.Fatalf("want error naming %s, got %v", JWTSecretEnv, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "short\n" {
		t.Fatal("corrupt secret file was overwritten")
	}
}

func TestResolveJWTSecretUnwritableDataDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced here")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, _, _, err := ResolveJWTSecret(JWTSecretOptions{DataDir: dir, AllowGenerate: true})
	if err == nil {
		t.Fatal("want error for unwritable data dir")
	}
	if !strings.Contains(err.Error(), JWTSecretEnv) || !strings.Contains(err.Error(), dir) {
		t.Fatalf("error should name the env var and the dir: %v", err)
	}
}

func TestResolveJWTSecretNoDataDir(t *testing.T) {
	_, _, _, err := ResolveJWTSecret(JWTSecretOptions{AllowGenerate: true})
	if err == nil || !strings.Contains(err.Error(), "YIPYAP_DATA_DIR") {
		t.Fatalf("want error naming YIPYAP_DATA_DIR, got %v", err)
	}
	if errors.Is(err, ErrJWTSecretRequired) {
		t.Fatal("no-data-dir error should differ from the required error")
	}
}

// withDriver sets the promoted ProConfig.DBDriver field, which a Config
// struct literal cannot name directly.
func withDriver(c Config, driver string) Config {
	c.DBDriver = driver
	return c
}

func TestResolveDataDir(t *testing.T) {
	stubUserDataDir(t, "/home/u/.config/yipyap")
	t.Chdir(t.TempDir())
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"explicit wins", Config{DataDir: "/srv/yy", DBDsn: "/data/yipyap.db"}, "/srv/yy"},
		{"sqlite absolute", Config{DBDsn: "/data/yipyap.db"}, "/data"},
		{"sqlite bare file uses the user dir", Config{DBDsn: "yipyap.db"}, "/home/u/.config/yipyap"},
		{"sqlite relative dir", Config{DBDsn: "state/yipyap.db"}, "state"},
		{"sqlite file uri", Config{DBDsn: "file:/var/lib/yy/db.sqlite?_pragma=busy_timeout(5000)"}, "/var/lib/yy"},
		{"sqlite memory", Config{DBDsn: ":memory:"}, "/home/u/.config/yipyap"},
		{"sqlite shared memory", Config{DBDsn: "file::memory:?cache=shared"}, "/home/u/.config/yipyap"},
		{"explicit sqlite driver", withDriver(Config{DBDsn: "/data/yipyap.db"}, "sqlite"), "/data"},
		{"postgres needs an explicit dir", withDriver(Config{DBDsn: "postgres://u:p@db:5432/yy"}, "postgres"), ""},
		{"postgres with explicit dir", withDriver(Config{DataDir: "/srv/yy", DBDsn: "postgres://u:p@db:5432/yy"}, "postgres"), "/srv/yy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.ResolveDataDir(); got != filepath.FromSlash(tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAPIKeyHMACSecretUsesResolvedJWTSecret(t *testing.T) {
	cfg := &Config{}
	dir := t.TempDir()
	secret, _, _, err := ResolveJWTSecret(JWTSecretOptions{Configured: cfg.JWTSecret, DataDir: dir, AllowGenerate: true})
	if err != nil {
		t.Fatal(err)
	}
	cfg.JWTSecret = secret

	got, fallback := cfg.APIKeyHMACSecret()
	if !fallback || got != secret || got == "" {
		t.Fatalf("fallback should use the resolved JWT secret (fallback=%v)", fallback)
	}

	cfg.APIKeySecret = validSecret
	got, fallback = cfg.APIKeyHMACSecret()
	if fallback || got != validSecret {
		t.Fatal("a dedicated API key secret should win")
	}
}

func TestResolveJWTSecretPostgresWithoutDataDirRequiresSecret(t *testing.T) {
	cfg := withDriver(Config{DBDsn: "postgres://u:p@db:5432/yy"}, "postgres")
	_, _, _, err := ResolveJWTSecret(JWTSecretOptions{DataDir: cfg.ResolveDataDir(), AllowGenerate: true})
	if err == nil || !strings.Contains(err.Error(), JWTSecretEnv) {
		t.Fatalf("want an error naming %s, got %v", JWTSecretEnv, err)
	}
}

func TestStoredSecretModeIsTightened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, JWTSecretFile)
	if err := os.WriteFile(path, []byte(validSecret+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	got, src, _, err := ResolveJWTSecret(JWTSecretOptions{DataDir: dir, AllowGenerate: true})
	if err != nil || got != validSecret || src != JWTSecretFromFile {
		t.Fatalf("got src=%v err=%v", src, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
	}
}

func stubUserDataDir(t *testing.T, dir string) {
	t.Helper()
	old := userDataDir
	userDataDir = func() string { return dir }
	t.Cleanup(func() { userDataDir = old })
}

// An install that already keeps jwt_secret in the working directory (bare
// SQLite DSN) keeps using it, so upgrading does not sign everyone out.
func TestResolveDataDirKeepsLegacyWorkingDirSecret(t *testing.T) {
	stubUserDataDir(t, "/home/u/.config/yipyap")
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(JWTSecretFile, []byte(strings.Repeat("a", 64)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (&Config{DBDsn: "yipyap.db"}).ResolveDataDir(); got != "." {
		t.Fatalf("got %q, want . (existing secret)", got)
	}
}

func TestResolveDataDirNoUserDir(t *testing.T) {
	stubUserDataDir(t, "")
	t.Chdir(t.TempDir())
	if got := (&Config{DBDsn: ":memory:"}).ResolveDataDir(); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// A generated secret is never written into a source checkout.
func TestResolveJWTSecretRefusesSourceTree(t *testing.T) {
	for _, marker := range []string{"go.mod", ".git"} {
		t.Run(marker, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, marker), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, _, err := ResolveJWTSecret(JWTSecretOptions{DataDir: dir, AllowGenerate: true})
			if err == nil || !strings.Contains(err.Error(), "YIPYAP_DATA_DIR") || !strings.Contains(err.Error(), marker) {
				t.Fatalf("err = %v, want a refusal naming YIPYAP_DATA_DIR and %s", err, marker)
			}
			if _, statErr := os.Stat(filepath.Join(dir, JWTSecretFile)); statErr == nil {
				t.Fatal("secret written into the source tree")
			}
		})
	}
}
