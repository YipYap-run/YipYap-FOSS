package oidc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	coreosoidc "github.com/coreos/go-oidc/v3/oidc"
)

// Sentinel errors for precise 401 vs 403 decisions at higher layers.
var (
	ErrTokenExpired     = errors.New("token expired")
	ErrWrongIssuer      = errors.New("wrong issuer")
	ErrWrongAudience    = errors.New("wrong audience")
	ErrInvalidSignature = errors.New("invalid signature")
	ErrJWKSUnavailable  = errors.New("JWKS unavailable")
	ErrMalformedToken   = errors.New("malformed token")
)

// VerifierConfig describes how to validate incoming JWT bearer tokens.
type VerifierConfig struct {
	// Issuer is the expected iss claim. When JWKSURL is empty, discovery
	// is performed against Issuer + "/.well-known/openid-configuration".
	Issuer string
	// JWKSURL, when non-empty, bypasses OIDC discovery and uses the given
	// endpoint directly. The iss claim is still compared against Issuer.
	JWKSURL string
	// Audiences lists acceptable values for the aud claim; at least one
	// must match.
	Audiences []string
	// RefreshWindow overrides the JWKS cache TTL. Zero uses the
	// coreos/go-oidc default which respects Cache-Control: max-age with
	// its own safety bounds. Currently honored only as a hint, the
	// underlying RemoteKeySet manages its own cache.
	RefreshWindow time.Duration
}

// IDToken is the subset of claims the verifier surfaces to callers.
type IDToken struct {
	Issuer   string
	Subject  string
	Audience []string
	Expiry   time.Time
	IssuedAt time.Time
	// Raw gives access to the original JSON claims for callers that need
	// more than the fields above.
	Raw map[string]any
}

// VerifierOption configures optional behaviour on a Verifier.
type VerifierOption func(*Verifier)

// WithNowFunc overrides the clock used for expiry checks. Test-only.
func WithNowFunc(now func() time.Time) VerifierOption {
	return func(v *Verifier) {
		if now != nil {
			v.now = now
		}
	}
}

// Verifier validates incoming JWTs from an external IdP (Knative cluster
// issuer, Dex, Keycloak, Authentik, etc.).
//
// Verifier is safe for concurrent use. The first call to Verify lazily
// performs OIDC discovery (or JWKS fetch), NewVerifier itself does not
// perform network I/O, so that construction-time ordering hazards can be
// avoided when the IdP is starting up alongside the service.
type Verifier struct {
	cfg VerifierConfig
	now func() time.Time

	mu sync.Mutex
	// tokenVerifier is the lazily-initialised coreos/go-oidc verifier.
	// Nil until the first successful init; failed inits leave it nil so
	// that subsequent Verify calls retry, a transient JWKS outage must
	// not poison the verifier for the remainder of the process lifetime.
	tokenVerifier *coreosoidc.IDTokenVerifier
}

// NewVerifier returns a Verifier for cfg. Discovery/JWKS fetch is
// deferred to the first Verify call.
func NewVerifier(cfg VerifierConfig, opts ...VerifierOption) *Verifier {
	v := &Verifier{
		cfg: cfg,
		now: time.Now,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// init performs OIDC discovery or bare JWKS fetch, producing an
// IDTokenVerifier with audience checking disabled (we enforce audiences
// ourselves to allow a list rather than a single value). It is invoked
// from Verify and is safe for concurrent use.
func (v *Verifier) init(ctx context.Context) (*coreosoidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.tokenVerifier != nil {
		return v.tokenVerifier, nil
	}
	tv, err := v.buildTokenVerifier(ctx)
	if err != nil {
		return nil, err
	}
	v.tokenVerifier = tv
	return tv, nil
}

func (v *Verifier) buildTokenVerifier(ctx context.Context) (*coreosoidc.IDTokenVerifier, error) {
	cfg := &coreosoidc.Config{
		// We enforce audience ourselves against v.cfg.Audiences.
		SkipClientIDCheck: true,
		Now:               v.now,
	}

	// coreos/go-oidc's RemoteKeySet and VerifierContext store the
	// context they're constructed with and reuse it for subsequent HTTP
	// fetches (JWKS refresh). When we're initialised from a short-lived
	// request context, that context is canceled as soon as the handler
	// returns, every later refresh then fails with "context canceled".
	// Use a detached background context for the long-lived client while
	// honoring the caller's ctx for the initial discovery roundtrip via
	// its deadline (preserved by oidc.ClientContext when provided).
	detached := context.Background()
	if cc := ctx.Value(coreosoidc.ClientContext); cc != nil { //nolint:staticcheck // SA1029: coreosoidc.ClientContext is a func, its address is used here as a context key by go-oidc's internal convention; mirror that on the detached ctx.
		// If the caller supplied a custom *http.Client via
		// oidc.ClientContext, preserve it on the detached context so
		// tests and plumbing still observe the configured transport.
		detached = context.WithValue(detached, coreosoidc.ClientContext, cc) //nolint:staticcheck // SA1029: see above.
	}

	if v.cfg.JWKSURL != "" {
		keySet := coreosoidc.NewRemoteKeySet(detached, v.cfg.JWKSURL)
		return coreosoidc.NewVerifier(v.cfg.Issuer, keySet, cfg), nil
	}

	// Discovery itself can legitimately fail on a short-lived ctx if the
	// IdP is slow, but we don't want the resulting Provider to later
	// refresh keys on a dead context, so issue discovery against
	// detached too. If a caller needs a deadline they should pass one
	// through on the Verify ctx, which remains scoped to request.
	provider, err := coreosoidc.NewProvider(detached, v.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrJWKSUnavailable, err.Error())
	}
	return provider.VerifierContext(detached, cfg), nil
}

// Verify parses and validates a raw JWT. Returns the verified token or an
// error wrapping one of the sentinel types so callers can distinguish
// expired tokens from audience mismatches from crypto failures.
func (v *Verifier) Verify(ctx context.Context, rawJWT string) (*IDToken, error) {
	// Fail fast on obviously malformed input before we pay for discovery.
	if !looksLikeJWT(rawJWT) {
		return nil, fmt.Errorf("%w: %q", ErrMalformedToken, truncate(rawJWT, 32))
	}

	tv, err := v.init(ctx)
	if err != nil {
		return nil, err
	}

	idt, err := tv.Verify(ctx, rawJWT)
	if err != nil {
		return nil, mapVerifyError(err)
	}

	// Audience: accept the token if any of its aud values matches any of
	// our configured audiences.
	if !audienceMatches(idt.Audience, v.cfg.Audiences) {
		return nil, fmt.Errorf("%w: token aud=%v, expected one of %v", ErrWrongAudience, idt.Audience, v.cfg.Audiences)
	}

	raw := map[string]any{}
	if err := idt.Claims(&raw); err != nil {
		// Claims() only fails if the payload isn't JSON, which can't
		// happen after a successful signature verify, but guard anyway.
		return nil, fmt.Errorf("%w: decode claims: %s", ErrMalformedToken, err.Error())
	}

	return &IDToken{
		Issuer:   idt.Issuer,
		Subject:  idt.Subject,
		Audience: idt.Audience,
		Expiry:   idt.Expiry,
		IssuedAt: idt.IssuedAt,
		Raw:      raw,
	}, nil
}

// looksLikeJWT returns true if s has the three dot-separated base64url
// segments of a JWS compact serialization.
func looksLikeJWT(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
	}
	return true
}

// audienceMatches returns true if any element of tokenAuds appears in
// expected. An empty expected set rejects all tokens (defensive: callers
// should never configure an empty audience list, but we don't want to
// fail open if they do).
func audienceMatches(tokenAuds, expected []string) bool {
	if len(expected) == 0 {
		return false
	}
	for _, a := range tokenAuds {
		for _, want := range expected {
			if a == want {
				return true
			}
		}
	}
	return false
}

// mapVerifyError converts coreos/go-oidc's verification errors into our
// sentinel types. The library exposes *TokenExpiredError as a typed error
// but leaves other cases as plain error strings, we pattern-match those.
func mapVerifyError(err error) error {
	var expired *coreosoidc.TokenExpiredError
	if errors.As(err, &expired) {
		return fmt.Errorf("%w: %s", ErrTokenExpired, err.Error())
	}

	msg := err.Error()
	lower := strings.ToLower(msg)

	switch {
	case strings.Contains(lower, "id token issued by a different provider") ||
		strings.Contains(lower, "issuer did not match") ||
		strings.Contains(lower, `"iss"`) && strings.Contains(lower, "mismatch") ||
		strings.Contains(lower, "wrong issuer"):
		return fmt.Errorf("%w: %s", ErrWrongIssuer, msg)
	case strings.Contains(lower, "expired"):
		return fmt.Errorf("%w: %s", ErrTokenExpired, msg)
	case strings.Contains(lower, "failed to verify signature") ||
		strings.Contains(lower, "failed to verify id token signature") ||
		strings.Contains(lower, "signature"):
		return fmt.Errorf("%w: %s", ErrInvalidSignature, msg)
	case strings.Contains(lower, "malformed jwt") ||
		strings.Contains(lower, "oidc: malformed") ||
		strings.Contains(lower, "failed to unmarshal") ||
		strings.Contains(lower, "illegal base64") ||
		strings.Contains(lower, "invalid character"):
		return fmt.Errorf("%w: %s", ErrMalformedToken, msg)
	case strings.Contains(lower, "fetch") && strings.Contains(lower, "keys"),
		strings.Contains(lower, "jwks"),
		strings.Contains(lower, "discovery"),
		strings.Contains(lower, "unable to fetch keys"):
		return fmt.Errorf("%w: %s", ErrJWKSUnavailable, msg)
	default:
		// Unknown failure, treat as signature failure (401) rather than
		// leaking an opaque error to higher layers. Callers still see the
		// underlying string via err.Error() for logging.
		return fmt.Errorf("%w: %s", ErrInvalidSignature, msg)
	}
}

// truncate shortens s to at most n runes for safe inclusion in error
// messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
