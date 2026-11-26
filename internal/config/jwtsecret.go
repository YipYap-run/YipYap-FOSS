package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// JWTSecretEnv is the environment variable that holds the JWT signing secret.
const JWTSecretEnv = "YIPYAP_JWT_SECRET"

// JWTSecretFile is the file name, inside the data dir, that holds a
// generated JWT signing secret.
const JWTSecretFile = "jwt_secret"

// MinJWTSecretLen is the minimum accepted secret length in bytes.
const MinJWTSecretLen = 32

// generatedSecretBytes is how many random bytes back a generated secret. The
// file stores them hex encoded (64 characters).
const generatedSecretBytes = 32

// knownPlaceholders are values that appear in published docs, compose files
// and examples. A secret equal to one of these is public, so it is refused.
var knownPlaceholders = []string{
	"change-me-in-production",
	"change-me",
	"changeme",
	"your-secret",
	"your-secret-here",
	"secret",
	"dev-secret",
}

// ErrJWTSecretRequired is returned by ResolveJWTSecret when no secret is set
// and generation is not allowed.
var ErrJWTSecretRequired = fmt.Errorf("%s is required: generate one with `openssl rand -hex 32` and set it in the environment", JWTSecretEnv)

// ValidateJWTSecret rejects known placeholder values and secrets shorter than
// MinJWTSecretLen bytes. The error names the env var and never includes the
// secret itself.
func ValidateJWTSecret(secret string) error {
	norm := strings.ToLower(strings.TrimSpace(secret))
	for _, p := range knownPlaceholders {
		if norm == p {
			return fmt.Errorf("%s is set to a published placeholder value; anyone can forge tokens with it. Generate a new one with `openssl rand -hex 32`, or unset %s so the server generates and stores one in the data dir", JWTSecretEnv, JWTSecretEnv)
		}
	}
	if len(secret) < MinJWTSecretLen {
		return fmt.Errorf("%s is %d bytes; it must be at least %d. Generate one with `openssl rand -hex 32`", JWTSecretEnv, len(secret), MinJWTSecretLen)
	}
	return nil
}

// JWTSecretOptions controls ResolveJWTSecret.
type JWTSecretOptions struct {
	// Configured is the value of YIPYAP_JWT_SECRET (empty when unset).
	Configured string
	// DataDir is where a generated secret is stored. Required when
	// AllowGenerate is true and Configured is empty.
	DataDir string
	// AllowGenerate permits generating and persisting a secret when
	// Configured is empty. The SaaS web binary sets this to false.
	AllowGenerate bool
}

// JWTSecretSource says where a resolved secret came from.
type JWTSecretSource int

const (
	JWTSecretFromEnv       JWTSecretSource = iota // set via YIPYAP_JWT_SECRET
	JWTSecretFromFile                             // read from an existing file in the data dir
	JWTSecretFromGenerated                        // generated on this boot and written to the data dir
)

// ResolveJWTSecret returns the JWT signing secret to use. A configured value
// is validated and used as-is. When nothing is configured and generation is
// allowed, the secret is read from <DataDir>/jwt_secret, or generated with
// crypto/rand and written there (mode 0600, atomically) on first boot. The
// returned path is the secret file when the secret came from the data dir.
func ResolveJWTSecret(opts JWTSecretOptions) (secret string, src JWTSecretSource, path string, err error) {
	if opts.Configured != "" {
		if err := ValidateJWTSecret(opts.Configured); err != nil {
			return "", 0, "", err
		}
		return opts.Configured, JWTSecretFromEnv, "", nil
	}
	if !opts.AllowGenerate {
		return "", 0, "", ErrJWTSecretRequired
	}
	if opts.DataDir == "" {
		return "", 0, "", fmt.Errorf("%s is not set and no data dir is known to store a generated one; set %s (required when several replicas share a database) or YIPYAP_DATA_DIR", JWTSecretEnv, JWTSecretEnv)
	}

	path = filepath.Join(opts.DataDir, JWTSecretFile)
	if s, err := readSecretFile(path); err == nil {
		return s, JWTSecretFromFile, path, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", 0, "", err
	}

	if marker := sourceTreeMarker(opts.DataDir); marker != "" {
		abs, _ := filepath.Abs(opts.DataDir)
		if abs == "" {
			abs = opts.DataDir
		}
		return "", 0, "", fmt.Errorf("%s is not set and the data dir %q looks like a source checkout (it contains %s); refusing to write a generated secret there. Set YIPYAP_DATA_DIR to a directory outside the source tree, or set %s", JWTSecretEnv, abs, marker, JWTSecretEnv)
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return "", 0, "", fmt.Errorf("%s is not set and the data dir %q cannot be created to store a generated one (%v); make it writable or set %s", JWTSecretEnv, opts.DataDir, err, JWTSecretEnv)
	}

	buf := make([]byte, generatedSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, "", fmt.Errorf("generate JWT secret: %w", err)
	}
	s := hex.EncodeToString(buf)

	created, err := writeSecretFileExclusive(path, s)
	if err != nil {
		return "", 0, "", fmt.Errorf("%s is not set and the generated secret cannot be written to %q (%v); make the data dir writable or set %s", JWTSecretEnv, path, err, JWTSecretEnv)
	}
	if !created {
		// Another process won the race. Use its secret so both agree.
		s, err := readSecretFile(path)
		if err != nil {
			return "", 0, "", err
		}
		return s, JWTSecretFromFile, path, nil
	}
	return s, JWTSecretFromGenerated, path, nil
}

// readSecretFile reads and validates a stored secret. A file that exists but
// holds an invalid value is an error, not a reason to overwrite it, since
// replacing it would silently log every user out.
func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("read JWT secret file %q: %w", path, err)
	}
	// A restored backup or a copy may have loosened the mode. Tighten it
	// back to owner-only; failure here is not fatal.
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(path, 0o600)
	}
	s := strings.TrimSpace(string(b))
	if len(s) < MinJWTSecretLen {
		return "", fmt.Errorf("JWT secret file %q is empty or shorter than %d bytes; delete it to generate a new one (this signs everyone out) or set %s", path, MinJWTSecretLen, JWTSecretEnv)
	}
	return s, nil
}

// writeSecretFileExclusive writes the secret to a temp file in the same dir,
// syncs it, then hard-links it into place so the final file appears complete
// or not at all and an existing file is never replaced. It reports false when
// the file already exists.
func writeSecretFileExclusive(path, secret string) (bool, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+JWTSecretFile+".tmp-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if _, err := tmp.WriteString(secret + "\n"); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}

	if err := os.Link(tmpName, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		// Some filesystems do not support hard links. Fall back to a rename,
		// which is still atomic but can replace a file created in between.
		if _, statErr := os.Lstat(path); statErr == nil {
			return false, nil
		}
		if err := os.Rename(tmpName, path); err != nil {
			return false, err
		}
	}
	syncDir(dir)
	return true, nil
}

// syncDir flushes the directory entry so the new file survives a crash.
// Best effort: not every platform supports fsync on a directory.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// APIKeyHMACSecret returns the key for API key and backup code HMACs:
// YIPYAP_API_KEY_SECRET when set, else the JWT secret. Call it after
// ResolveJWTSecret has stored the resolved secret in c.JWTSecret, so the
// fallback never uses an empty or unvalidated value. fallback reports whether
// the JWT secret was used.
func (c *Config) APIKeyHMACSecret() (secret string, fallback bool) {
	if c.APIKeySecret != "" {
		return c.APIKeySecret, false
	}
	return c.JWTSecret, true
}

// ResolveDataDir returns the directory for local state such as a generated
// JWT secret, in this order:
//
//  1. YIPYAP_DATA_DIR.
//  2. The directory of a SQLite database file whose DSN names a directory
//     (for example /data/yipyap.db in the Docker image).
//  3. "." when a jwt_secret already exists there, so installs that ran with
//     a bare DSN such as yipyap.db keep their secret.
//  4. A per-user dir, os.UserConfigDir()/yipyap.
//
// With any other driver it returns "" unless YIPYAP_DATA_DIR is set: a
// shared database usually means several replicas, and a secret generated
// into each one's local disk would differ, so tokens from one replica would
// fail on the next. The caller then requires an explicit secret. It also
// returns "" when no per-user dir is known.
func (c *Config) ResolveDataDir() string {
	if c.DataDir != "" {
		return c.DataDir
	}
	if c.DBDriver != "" && c.DBDriver != "sqlite" {
		return ""
	}
	if p := sqliteFilePath(c.DBDsn); p != "" {
		if dir := filepath.Dir(p); dir != "." {
			return dir
		}
	}
	if _, err := os.Stat(JWTSecretFile); err == nil {
		return "."
	}
	return userDataDir()
}

// userDataDir is os.UserConfigDir()/yipyap, or "" when the platform has no
// per-user config dir (for example no $HOME). A variable so tests can stub it.
var userDataDir = func() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return ""
	}
	return filepath.Join(base, "yipyap")
}

// sourceTreeMarker returns the name of a go.mod or .git entry in dir, or ""
// when there is none. A generated secret must not land in a source checkout,
// where it can be committed or shipped by accident.
func sourceTreeMarker(dir string) string {
	for _, name := range []string{"go.mod", ".git"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return ""
}

// sqliteFilePath extracts the file path from a SQLite DSN such as
// "yipyap.db", "/data/yipyap.db" or "file:/data/yipyap.db?_pragma=...".
// It returns "" for in-memory databases.
func sqliteFilePath(dsn string) string {
	p := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p == "" || p == ":memory:" || strings.HasPrefix(p, ":memory:") {
		return ""
	}
	return p
}
