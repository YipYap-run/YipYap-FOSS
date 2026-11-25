// Package session keeps server-side records of login sessions so a session
// JWT can be revoked before it expires. Every login path issues a
// user_sessions row and a JWT whose sid claim names it. The auth middleware
// calls Check on each request; logout, password changes, role changes,
// disable and deletion revoke rows.
//
// Check caches results in process for CacheTTL so the hot path does not hit
// the database on every request. A revocation made on this replica purges
// the cache at once; one made on another replica lands within CacheTTL.
package session

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const (
	// CacheTTL bounds how long a cached Check result is trusted. It must
	// stay well under 30 seconds so cross-replica revocation lands quickly.
	CacheTTL = 5 * time.Second
	// TouchInterval is the minimum gap between last_seen_at writes.
	TouchInterval = time.Minute
	// maxCacheEntries caps the in-process cache.
	maxCacheEntries = 50000
	maxUserAgentLen = 512
)

// nowFunc is the clock for Check and the cache; tests replace it.
var nowFunc = time.Now

// ErrRevoked is returned by Check when the token's session is missing,
// revoked, expired or does not match the token.
var ErrRevoked = errors.New("session revoked or expired")

// ErrUnsupported is returned when the store does not keep sessions.
var ErrUnsupported = errors.New("store does not support sessions")

func sessions(s store.Store) (store.SessionStore, error) {
	p, ok := s.(store.SessionProvider)
	if !ok {
		return nil, ErrUnsupported
	}
	return p.Sessions(), nil
}

// ClientIP returns the request's client address without the port. The
// server's trusted-proxy middleware has already rewritten RemoteAddr.
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func userAgent(r *http.Request) string {
	if r == nil {
		return ""
	}
	ua := r.UserAgent()
	if len(ua) > maxUserAgentLen {
		ua = ua[:maxUserAgentLen]
	}
	return ua
}

// Issue creates a session row for a completed login and returns the signed
// session JWT. amr is one of the auth.AMR* values and is stored as the
// session's auth method.
func Issue(ctx context.Context, s store.Store, jwt *auth.JWTIssuer, r *http.Request, userID, orgID, role, amr string) (string, error) {
	return issue(ctx, s, jwt, r, userID, orgID, role, amr, amr, jwt.TTL())
}

// issue creates the session row and the signed token. amr is one of the
// auth.AMR* values and is stored as the session's auth method unless method
// overrides it.
func issue(ctx context.Context, s store.Store, jwt *auth.JWTIssuer, r *http.Request, userID, orgID, role, amr, method string, ttl time.Duration) (string, error) {
	ss, err := sessions(s)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	now := time.Now().UTC().Truncate(time.Second)
	sess := &domain.Session{
		ID:         uuid.New().String(),
		UserID:     userID,
		OrgID:      orgID,
		AuthMethod: method,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(ttl),
		UserAgent:  userAgent(r),
		IP:         ClientIP(r),
	}
	if err := ss.Create(ctx, sess); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	return jwt.IssueSessionToken(userID, orgID, role, amr, sess.ID, sess.ExpiresAt)
}

// Renew re-signs the caller's session with a fresh expiry and the given
// role, keeping the same session ID so revocation still applies.
func Renew(ctx context.Context, s store.Store, jwt *auth.JWTIssuer, claims *auth.Claims, role string) (string, error) {
	if claims == nil || claims.SID == "" {
		return "", ErrRevoked
	}
	ss, err := sessions(s)
	if err != nil {
		return "", err
	}
	exp := time.Now().UTC().Truncate(time.Second).Add(jwt.TTL())
	if err := ss.Extend(ctx, claims.SID, exp); err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return "", ErrRevoked
		}
		return "", err
	}
	forget(claims.SID)
	return jwt.IssueSessionToken(claims.UserID, claims.OrgID, role, claims.AMR, claims.SID, exp)
}

// Check reports whether the token's session is still active. A token
// without a sid claim is rejected.
func Check(ctx context.Context, s store.Store, claims *auth.Claims) error {
	if claims == nil || claims.SID == "" {
		return ErrRevoked
	}
	now := nowFunc()
	if e, ok := lookup(claims.SID, now); ok {
		if !e.valid(claims, now) {
			return ErrRevoked
		}
		maybeTouch(ctx, s, claims.SID, e, now)
		return nil
	}
	ss, err := sessions(s)
	if err != nil {
		return err
	}
	sess, err := ss.Get(ctx, claims.SID)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			remember(claims.SID, entry{revoked: true, checkedAt: now})
			return ErrRevoked
		}
		return err
	}
	e := entry{
		userID:    sess.UserID,
		orgID:     sess.OrgID,
		expiresAt: sess.ExpiresAt,
		lastSeen:  sess.LastSeenAt,
		revoked:   sess.RevokedAt != nil,
		checkedAt: now,
	}
	remember(claims.SID, e)
	if !e.valid(claims, now) {
		return ErrRevoked
	}
	maybeTouch(ctx, s, claims.SID, e, now)
	return nil
}

// Checker adapts Check to the auth middleware's session hook.
func Checker(s store.Store) func(context.Context, *auth.Claims) error {
	return func(ctx context.Context, c *auth.Claims) error { return Check(ctx, s, c) }
}

func maybeTouch(ctx context.Context, s store.Store, sid string, e entry, now time.Time) {
	if now.Sub(e.lastSeen) < TouchInterval {
		return
	}
	ss, err := sessions(s)
	if err != nil {
		return
	}
	at := now.UTC().Truncate(time.Second)
	if err := ss.Touch(ctx, sid, at, at.Add(-TouchInterval)); err != nil {
		return
	}
	cacheMu.Lock()
	if cur, ok := cache[sid]; ok {
		cur.lastSeen = at
		cache[sid] = cur
	}
	cacheMu.Unlock()
}

// Revoke revokes one of userID's sessions. It returns
// store.ErrSessionNotFound when the session is not the user's or is
// already revoked.
func Revoke(ctx context.Context, s store.Store, userID, sid string) error {
	ss, err := sessions(s)
	if err != nil {
		return err
	}
	err = ss.Revoke(ctx, userID, sid, time.Now().UTC())
	forget(sid)
	return err
}

// RevokeAll revokes every session of userID except exceptSID ("" revokes
// all of them).
func RevokeAll(ctx context.Context, s store.Store, userID, exceptSID string) error {
	ss, err := sessions(s)
	if err != nil {
		return err
	}
	ids, err := ss.RevokeAllForUser(ctx, userID, exceptSID, time.Now().UTC())
	for _, id := range ids {
		forget(id)
	}
	forgetUser(userID, exceptSID)
	return err
}

// RevokeOrg revokes the sessions of org members that f does not keep and
// returns how many it revoked. A session created on another replica while
// this runs is still revoked by the store; its cached check lapses within
// CacheTTL.
func RevokeOrg(ctx context.Context, s store.Store, orgID string, f store.OrgSessionFilter) (int, error) {
	ss, err := sessions(s)
	if err != nil {
		return 0, err
	}
	ids, err := ss.RevokeOrgSessions(ctx, orgID, f, time.Now().UTC())
	for _, id := range ids {
		forget(id)
	}
	return len(ids), err
}

// SignInTime returns when the caller's session was signed in, if the
// session is active, belongs to the caller and was created by a sign-in with
// the given auth method. It reads the session row, not the token, because a
// refresh re-signs the token without a new sign-in.
func SignInTime(ctx context.Context, s store.Store, claims *auth.Claims, method string) (time.Time, bool, error) {
	if claims == nil || claims.SID == "" || claims.AMR != method {
		return time.Time{}, false, nil
	}
	ss, err := sessions(s)
	if err != nil {
		return time.Time{}, false, err
	}
	sess, err := ss.Get(ctx, claims.SID)
	if err != nil {
		if errors.Is(err, store.ErrSessionNotFound) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, err
	}
	if !sess.Active(nowFunc()) || sess.UserID != claims.UserID || sess.OrgID != claims.OrgID || sess.AuthMethod != method {
		return time.Time{}, false, nil
	}
	return sess.CreatedAt, true, nil
}

// SignedInRecently reports whether SignInTime finds a sign-in with method
// no more than within ago.
func SignedInRecently(ctx context.Context, s store.Store, claims *auth.Claims, method string, within time.Duration) (bool, error) {
	at, ok, err := SignInTime(ctx, s, claims, method)
	if err != nil || !ok {
		return false, err
	}
	age := nowFunc().Sub(at)
	return age >= -time.Minute && age <= within, nil
}

// List returns the user's active sessions, newest first.
func List(ctx context.Context, s store.Store, userID string) ([]*domain.Session, error) {
	ss, err := sessions(s)
	if err != nil {
		return nil, err
	}
	return ss.ListActiveByUser(ctx, userID, time.Now().UTC())
}

// PruneExpired deletes sessions that expired before the cutoff.
func PruneExpired(ctx context.Context, s store.Store, before time.Time) (int64, error) {
	ss, err := sessions(s)
	if err != nil {
		return 0, err
	}
	return ss.DeleteExpired(ctx, before)
}

// In-process cache.

type entry struct {
	userID    string
	orgID     string
	expiresAt time.Time
	lastSeen  time.Time
	revoked   bool
	checkedAt time.Time
}

func (e entry) valid(c *auth.Claims, now time.Time) bool {
	return !e.revoked && now.Before(e.expiresAt) && e.userID == c.UserID && e.orgID == c.OrgID
}

var (
	cacheMu sync.Mutex
	cache   = make(map[string]entry)
)

func lookup(sid string, now time.Time) (entry, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[sid]
	if !ok {
		return entry{}, false
	}
	if now.Sub(e.checkedAt) >= CacheTTL {
		delete(cache, sid)
		return entry{}, false
	}
	return e, true
}

func remember(sid string, e entry) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cache) >= maxCacheEntries {
		now := nowFunc()
		for k, v := range cache {
			if now.Sub(v.checkedAt) >= CacheTTL {
				delete(cache, k)
			}
		}
		if len(cache) >= maxCacheEntries {
			cache = make(map[string]entry)
		}
	}
	cache[sid] = e
}

func forget(sid string) {
	cacheMu.Lock()
	delete(cache, sid)
	cacheMu.Unlock()
}

// forgetUser drops every cached entry of userID except keepSID, covering
// sessions created after RevokeAllForUser listed its IDs.
func forgetUser(userID, keepSID string) {
	cacheMu.Lock()
	for k, v := range cache {
		if v.userID == userID && k != keepSID {
			delete(cache, k)
		}
	}
	cacheMu.Unlock()
}
