package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// The CloudEvents poll and stream API read the fan-out log by sequence.
// Delivery contract (knative decision 1, at-least-once):
//
//   - Every delivered event carries its own cursor in the `yipyapcursor`
//     extension attribute; a poll response also carries `next_cursor`,
//     the cursor of the last log entry the read covered.
//   - A request with since=<cursor> returns events after that cursor. The
//     server never consumes or acks anything on read, so re-sending an
//     older cursor redelivers everything after it. Sending a cursor is the
//     client's confirmation that everything up to it was handled.
//   - POST /cloudevents/ack confirms a cursor for a source ID without a
//     read, for stream clients that stay connected (cloudevents_ack.go).
//   - Every request must carry X-YipYap-Source-ID. The server remembers a
//     position per source, and that position moves only when the client
//     confirms a cursor. A source that never confirms gets the same events
//     again, and two sources on one org never split a batch.
//     Positions are persisted in the store so a reconnect on another replica
//     resumes where the ack left off.

// CESourceIDHeader names the header a client uses to identify itself, for
// example the YipYapSource UID. Two sources on one org each keep their own
// position and each see every event. It is required on every poll, stream and
// ack request.
const CESourceIDHeader = "X-YipYap-Source-ID"

// ceCursorExtension is the CloudEvents extension attribute that carries an
// event's cursor. Extension names are lowercase alphanumeric.
const ceCursorExtension = "yipyapcursor"

// ceCursorVersion marks the sequence-based cursor format. Cursors without
// it come from the old counter-based handler and are ignored.
const ceCursorVersion = 2

// Input limits.
const (
	ceMaxCursorLen   = 512
	ceMaxSourceIDLen = 128
	ceMaxMonitorIDs  = 100
	ceMaxTypeFilters = 50
	// ceMaxCachedAlerts bounds the per-connection alert severity cache.
	ceMaxCachedAlerts = 1000
)

// ceAlertTypePrefix marks the event types the severity filter applies to.
const ceAlertTypePrefix = "run.yipyap.alert."

// Remembered-position bounds.
const (
	// DefaultCEPositionsPerOrg caps remembered positions per org; the least
	// recently used one is dropped past it, in both backends.
	DefaultCEPositionsPerOrg = 64
	// DefaultCEPositionTTL matches the fan-out stream's MaxAge: a position
	// older than this points at events that are gone anyway.
	DefaultCEPositionTTL = 24 * time.Hour
)

// errInvalidConsumerOrg is returned for an org ID that is not a single NATS
// subject token. The reader filters the fan-out tree by org ID, so a
// wildcard or dot in it would widen the filter to other orgs' events.
var errInvalidConsumerOrg = errors.New("invalid org id for cloudevents reader")

// validSubjectToken reports whether s can be used as one literal NATS
// subject token: non-empty, no separators, wildcards or whitespace.
func validSubjectToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, ".*> \t\r\n")
}

// validSeverities is the set accepted by the severity filter.
var validSeverities = map[string]bool{
	string(domain.SeverityCritical): true,
	string(domain.SeverityWarning):  true,
	string(domain.SeverityInfo):     true,
}

// ceCursor is the decoded form of a cursor: the log epoch, the sequence of
// the last entry covered, and, to map the cursor onto a log with another
// epoch, when that entry was appended (T, unix ms) and its CloudEvent ID
// (I). T and I are empty when the cursor covers no entry yet; T is then
// the time the position was taken.
type ceCursor struct {
	V int    `json:"v"`
	E string `json:"e"`
	S uint64 `json:"s"`
	T int64  `json:"t,omitempty"`
	I string `json:"i,omitempty"`
}

// ceMaxAnchorIDLen bounds the event ID carried in a cursor. A longer ID is
// left out and the cursor falls back to its time.
const ceMaxAnchorIDLen = 128

// ceSeekSkew widens the time window used to map a cursor onto a log with
// another epoch, to absorb clock skew and delivery lag between replicas.
// Entries in the window are delivered again unless the cursor's event is
// found in it.
const ceSeekSkew = 5 * time.Second

func (c ceCursor) encode() string {
	c.V = ceCursorVersion
	if len(c.I) > ceMaxAnchorIDLen {
		c.I = ""
	}
	b, _ := json.Marshal(c)
	out := base64.RawURLEncoding.EncodeToString(b)
	if len(out) > ceMaxCursorLen && c.I != "" {
		// JSON escaping can grow an ID past what decode accepts; the
		// server must never issue a cursor it would reject.
		c.I = ""
		b, _ = json.Marshal(c)
		out = base64.RawURLEncoding.EncodeToString(b)
	}
	return out
}

func encodeCECursor(epoch string, seq uint64) string {
	return ceCursor{E: epoch, S: seq}.encode()
}

// errBadCursor is returned for a cursor that is not one this server issued.
var errBadCursor = errors.New("invalid cursor")

// decodeCECursor parses a cursor. A cursor in the old counter format
// returns (nil, nil): it carries no position, so it is treated as absent.
func decodeCECursor(raw string) (*ceCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > ceMaxCursorLen {
		return nil, errBadCursor
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, errBadCursor
	}
	var c ceCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, errBadCursor
	}
	if c.V == 0 {
		return nil, nil
	}
	if c.V != ceCursorVersion || c.E == "" || c.T < 0 || len(c.I) > ceMaxAnchorIDLen {
		return nil, errBadCursor
	}
	return &c, nil
}

// ceFilter is the parsed event filter for one request. All present parts
// must match: type globs (any of), severities (alert events only) and
// monitor IDs (any of the event's monitors).
type ceFilter struct {
	types      []string
	severities map[string]bool
	monitors   map[string]bool
}

// ceRequest is a validated poll or stream request.
type ceRequest struct {
	orgID string
	// key identifies the remembered position of the source.
	key    string
	since  *ceCursor
	filter ceFilter
}

type ceGap struct {
	After          uint64 `json:"after"`
	FirstAvailable uint64 `json:"first_available"`
}

func gapAfter(pos ceCursor, st bus.LogState) *ceGap {
	if st.FirstSeq == 0 || pos.S+1 >= st.FirstSeq {
		return nil
	}
	return &ceGap{After: pos.S, FirstAvailable: st.FirstSeq}
}

// CloudEventsFeed is shared by the poll and stream handlers. It owns the
// log reader, the request filters and the remembered positions.
type CloudEventsFeed struct {
	log       bus.EventLog
	store     store.Store
	positions cePositionBackend
}

// NewCloudEventsFeed builds the feed for b. A bus with a retained log
// (JetStream) is read directly; any other bus gets an in-process log fed
// from the fan-out subjects. If that subscription fails, poll and stream
// answer 503 and the rest of the API is unaffected. s resolves monitor IDs
// and alert severities for the filters; with a nil store monitor_id
// filters are rejected.
func NewCloudEventsFeed(b bus.Bus, s store.Store) *CloudEventsFeed {
	l, ok := bus.AsEventLog(b)
	if !ok {
		mem, err := bus.NewMemoryEventLog(b, cepub.FanoutSubjectFor("*", ">"), 0, 0)
		if err != nil {
			slog.Error("cloudevents feed: subscribe to fan-out subjects failed; poll and stream are disabled",
				"error", err)
			return newCloudEventsFeedWithLog(unavailableLog{}, s)
		}
		l = mem
	}
	return newCloudEventsFeedWithLog(l, s)
}

// unavailableLog fails every call with ErrEventLogUnavailable.
type unavailableLog struct{}

func (unavailableLog) LogState(context.Context) (bus.LogState, error) {
	return bus.LogState{}, bus.ErrEventLogUnavailable
}

func (unavailableLog) ReadLog(context.Context, string, uint64, int, time.Duration) ([]bus.LogEntry, error) {
	return nil, bus.ErrEventLogUnavailable
}

func newCloudEventsFeedWithLog(l bus.EventLog, s store.Store) *CloudEventsFeed {
	var positions cePositionBackend = newCEPositionStore(DefaultCEPositionsPerOrg, DefaultCEPositionTTL)
	if s != nil {
		if persisted := s.CloudEventPositions(); persisted != nil {
			positions = newCESQLPositionStore(persisted, DefaultCEPositionTTL, DefaultCEPositionsPerOrg)
		}
	}
	return &CloudEventsFeed{log: l, store: s, positions: positions}
}

// parseRequest validates the shared poll/stream query and headers. On a
// problem it writes the error response and returns nil.
func (f *CloudEventsFeed) parseRequest(w http.ResponseWriter, r *http.Request) *ceRequest {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.OrgID == "" {
		errorResponse(w, http.StatusUnauthorized, "unauthorized")
		return nil
	}
	if !validSubjectToken(claims.OrgID) {
		slog.Warn("cloudevents feed: org id is not a subject token", "error", errInvalidConsumerOrg)
		errorResponse(w, http.StatusInternalServerError, "consumer subscribe failed")
		return nil
	}
	q := r.URL.Query()
	req := &ceRequest{orgID: claims.OrgID}

	req.filter.types = cleanFilters(q["filter"])
	if len(req.filter.types) > ceMaxTypeFilters {
		errorResponse(w, http.StatusBadRequest, "too many filter values")
		return nil
	}

	for _, raw := range q["severity"] {
		s := strings.ToLower(strings.TrimSpace(raw))
		if !validSeverities[s] {
			errorResponse(w, http.StatusBadRequest, "severity must be one of critical, warning, info")
			return nil
		}
		if req.filter.severities == nil {
			req.filter.severities = map[string]bool{}
		}
		req.filter.severities[s] = true
	}

	ids := q["monitor_id"]
	if len(ids) > ceMaxMonitorIDs {
		errorResponse(w, http.StatusBadRequest, "too many monitor_id values")
		return nil
	}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			errorResponse(w, http.StatusBadRequest, "monitor_id must not be empty")
			return nil
		}
		if req.filter.monitors[id] {
			continue
		}
		ok, err := f.monitorInOrg(r.Context(), claims.OrgID, id)
		if err != nil {
			// A store failure is not the client's fault. Answering 400
			// would tell an adapter its filter is invalid and stop it
			// retrying.
			slog.Warn("cloudevents feed: monitor lookup failed", "error", err)
			errorResponse(w, http.StatusInternalServerError, "monitor lookup failed")
			return nil
		}
		if !ok {
			// Same answer for a missing monitor and another org's
			// monitor, so the endpoint cannot probe IDs.
			errorResponse(w, http.StatusBadRequest, "unknown monitor_id")
			return nil
		}
		if req.filter.monitors == nil {
			req.filter.monitors = map[string]bool{}
		}
		req.filter.monitors[id] = true
	}

	since, err := decodeCECursor(q.Get("since"))
	if err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid since cursor")
		return nil
	}
	req.since = since

	if raw := r.Header.Get(CESourceIDHeader); raw != "" {
		if !validSourceID(raw) {
			errorResponse(w, http.StatusBadRequest, "invalid "+CESourceIDHeader+" header")
			return nil
		}
		req.key = "source\x00" + raw
	} else {
		errorResponse(w, http.StatusBadRequest, CESourceIDHeader+" header is required (1 to 128 chars of [A-Za-z0-9._:/-])")
		return nil
	}
	return req
}

// validSourceID accepts the characters of Kubernetes UIDs and names plus
// a few separators, up to ceMaxSourceIDLen.
func validSourceID(s string) bool {
	if len(s) == 0 || len(s) > ceMaxSourceIDLen {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '_', c == '.', c == ':', c == '/':
		default:
			return false
		}
	}
	return true
}

// monitorInOrg reports whether monitor id exists in orgID. A missing
// monitor is (false, nil); any other store error is returned.
func (f *CloudEventsFeed) monitorInOrg(ctx context.Context, orgID, id string) (bool, error) {
	if f.store == nil {
		return false, nil
	}
	m, err := f.store.Monitors().GetByID(ctx, id)
	if err != nil {
		// The SQL stores report a missing row as "monitor not found"
		// with no sentinel error.
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return m != nil && m.OrgID == orgID, nil
}

// subject is the fan-out subject filter for the request's org.
func (req *ceRequest) subject() string {
	return cepub.FanoutSubjectFor(req.orgID, ">")
}

// start resolves the position a read begins after and records any
// confirmation the request carries. The returned cursor is in the log's
// current epoch; the gap is non-nil when retention dropped entries after it.
func (f *CloudEventsFeed) start(ctx context.Context, req *ceRequest) (ceCursor, *ceGap, error) {
	st, err := f.log.LogState(ctx)
	if err != nil {
		return ceCursor{}, nil, err
	}
	var pos ceCursor
	if req.since != nil {
		pos = f.resolve(req, st, *req.since)
	} else if c, ok := f.positions.load(ctx, req.orgID, req.key); ok {
		pos = f.resolve(req, st, c)
	} else {
		// A new source starts at the current end of the log.
		pos = ceCursor{E: st.Epoch, S: st.LastSeq, T: time.Now().UnixMilli()}
	}
	// Remember the position. A source's position moves only when the client
	// confirms a cursor, so a retry without one gets the same events again.
	f.positions.save(ctx, req.orgID, req.key, pos)
	return pos, gapAfter(pos, st), nil
}

// resolve maps cursor c onto the log's current epoch.
func (f *CloudEventsFeed) resolve(req *ceRequest, st bus.LogState, c ceCursor) ceCursor {
	c.V = ceCursorVersion
	if c.E == st.Epoch {
		if c.S > st.LastSeq {
			c.S = st.LastSeq
		}
		return c
	}
	pos := ceCursor{V: ceCursorVersion, E: st.Epoch, T: c.T, I: c.I}
	// The cursor comes from another incarnation of the log: a recreated
	// stream, a restarted process, or another web replica on a bus
	// without a shared log. Its numbering means nothing here.
	seeker, ok := f.log.(bus.EventSeeker)
	if !ok || c.T == 0 {
		// Replay what is retained rather than skip it.
		return pos
	}
	// Resume right after the cursor's event if this log holds it, else
	// from shortly before the cursor's time. Without this, a client
	// whose polls alternate between replicas would be sent back to the
	// start of the retained log on every switch and never progress.
	var anchor func([]byte) bool
	if c.I != "" {
		anchor = eventIDIs(c.I)
	}
	pos.S = seeker.SeekAfter(req.subject(), time.UnixMilli(c.T).Add(-ceSeekSkew), anchor)
	return pos
}

// eventIDIs returns a predicate that reports whether a raw CloudEvent has
// the given ID.
func eventIDIs(id string) func([]byte) bool {
	quoted, _ := json.Marshal(id)
	return func(raw []byte) bool {
		if !bytes.Contains(raw, quoted) {
			return false
		}
		var env struct {
			ID string `json:"id"`
		}
		return json.Unmarshal(raw, &env) == nil && env.ID == id
	}
}

// ceMatcher applies a request's filter to raw events. It caches alert
// severity lookups for its lifetime (one poll, or one stream connection).
type ceMatcher struct {
	feed   *CloudEventsFeed
	req    *ceRequest
	alerts map[string]string
}

func (f *CloudEventsFeed) matcher(req *ceRequest) *ceMatcher {
	return &ceMatcher{feed: f, req: req, alerts: map[string]string{}}
}

// ceEnvelope is the part of a structured-mode CloudEvent the filters read.
type ceEnvelope struct {
	SpecVersion string          `json:"specversion"`
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Data        json.RawMessage `json:"data"`
}

// ceEventData holds the payload fields the filters read. Alert and monitor
// events carry monitor_id; maintenance.started lists monitors.
type ceEventData struct {
	AlertID   string   `json:"alert_id"`
	MonitorID string   `json:"monitor_id"`
	Severity  string   `json:"severity"`
	Monitors  []string `json:"monitors"`
}

// match reports whether raw is a well-formed CloudEvent that passes the
// request's filter.
func (m *ceMatcher) match(ctx context.Context, raw []byte) bool {
	_, ok := m.matchID(ctx, raw)
	return ok
}

// matchID is match that also returns the event's ID, empty when raw does
// not decode.
func (m *ceMatcher) matchID(ctx context.Context, raw []byte) (string, bool) {
	var env ceEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", false
	}
	return env.ID, m.matchEnvelope(ctx, env)
}

func (m *ceMatcher) matchEnvelope(ctx context.Context, env ceEnvelope) bool {
	if env.SpecVersion == "" || env.ID == "" || env.Type == "" {
		return false
	}
	flt := m.req.filter
	if len(flt.types) > 0 && !cepub.MatchAnyGlob(env.Type, flt.types) {
		return false
	}
	if len(flt.severities) == 0 && len(flt.monitors) == 0 {
		return true
	}
	var data ceEventData
	if len(env.Data) > 0 && env.Data[0] == '{' {
		_ = json.Unmarshal(env.Data, &data)
	}
	if len(flt.monitors) > 0 {
		hit := flt.monitors[data.MonitorID]
		for _, id := range data.Monitors {
			hit = hit || flt.monitors[id]
		}
		if !hit {
			return false
		}
	}
	if len(flt.severities) > 0 && strings.HasPrefix(env.Type, ceAlertTypePrefix) {
		sev := data.Severity
		if sev == "" && data.AlertID != "" {
			sev = m.alertSeverity(ctx, data.AlertID)
		}
		// An alert whose severity cannot be found is delivered: a
		// filter must not drop an event it cannot classify.
		if sev != "" && !flt.severities[sev] {
			return false
		}
	}
	return true
}

// alertSeverity looks up an alert's severity for alert events whose payload
// does not carry it (acknowledged, resolved, escalated).
func (m *ceMatcher) alertSeverity(ctx context.Context, alertID string) string {
	if sev, ok := m.alerts[alertID]; ok {
		return sev
	}
	sev := ""
	if m.feed.store != nil {
		a, err := m.feed.store.Alerts().GetByID(ctx, alertID)
		if err == nil && a != nil && a.OrgID == m.req.orgID {
			sev = string(a.Severity)
		}
	}
	if len(m.alerts) >= ceMaxCachedAlerts {
		// A long-lived stream sees many alerts; start over rather
		// than grow without bound.
		clear(m.alerts)
	}
	m.alerts[alertID] = sev
	return sev
}

// withCursor adds the cursor extension attribute to a structured-mode
// CloudEvent JSON object.
func withCursor(raw []byte, cursor string) []byte {
	trimmed := bytes.TrimRight(raw, " \t\r\n")
	if len(trimmed) < 2 || trimmed[len(trimmed)-1] != '}' {
		return raw
	}
	val, _ := json.Marshal(cursor)
	out := make([]byte, 0, len(trimmed)+len(ceCursorExtension)+len(val)+4)
	body := bytes.TrimRight(trimmed[:len(trimmed)-1], " \t\r\n")
	out = append(out, body...)
	if len(body) > 1 {
		out = append(out, ',')
	}
	out = append(out, '"')
	out = append(out, ceCursorExtension...)
	out = append(out, '"', ':')
	out = append(out, val...)
	out = append(out, '}')
	return out
}

// ceBatch is one read's worth of filtered events.
type ceBatch struct {
	events []json.RawMessage
	// next covers the last log entry the read covered, including
	// entries the filter dropped.
	next ceCursor
}

// read collects up to max matching events after the given sequence. It
// returns as soon as at least one event matched, or when wait runs out.
// Entries the filter drops still advance next, so a narrow filter does not
// re-read them.
func (f *CloudEventsFeed) read(ctx context.Context, req *ceRequest, m *ceMatcher, after ceCursor, max int, wait time.Duration) (ceBatch, error) {
	batch := ceBatch{events: []json.RawMessage{}, next: after}
	deadline := time.Now().Add(wait)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 || ctx.Err() != nil {
			return batch, nil
		}
		entries, err := f.log.ReadLog(ctx, req.subject(), batch.next.S, max-len(batch.events), remaining)
		if err != nil {
			return batch, err
		}
		if len(entries) == 0 {
			return batch, nil
		}
		for _, e := range entries {
			id, ok := m.matchID(ctx, e.Data)
			batch.next = ceCursor{V: ceCursorVersion, E: after.E, S: e.Seq, T: e.Time.UnixMilli(), I: id}
			if e.Time.IsZero() {
				batch.next.T = 0
			}
			if !ok {
				continue
			}
			batch.events = append(batch.events, json.RawMessage(withCursor(e.Data, batch.next.encode())))
		}
		if len(batch.events) > 0 {
			return batch, nil
		}
	}
}

// feedErrorResponse maps a log failure to a response.
func feedErrorResponse(w http.ResponseWriter, err error) {
	if errors.Is(err, bus.ErrEventLogUnavailable) {
		w.Header().Set("Retry-After", "60")
		errorResponse(w, http.StatusServiceUnavailable, "event feed unavailable")
		return
	}
	slog.Warn("cloudevents feed: read failed", "error", err)
	errorResponse(w, http.StatusInternalServerError, "event feed read failed")
}

type cePositionBackend interface {
	load(ctx context.Context, orgID, key string) (ceCursor, bool)
	save(ctx context.Context, orgID, key string, pos ceCursor)
}

// cePosition is one remembered position.
type cePosition struct {
	cursor   ceCursor
	lastUsed time.Time
}

// cePositionStore remembers a read position per (org, key) in process.
// Entries expire after ttl and each org keeps at most perOrg of them, so a
// caller cycling source IDs cannot grow it without bound.
type cePositionStore struct {
	mu     sync.Mutex
	perOrg int
	ttl    time.Duration
	clock  func() time.Time
	orgs   map[string]map[string]*cePosition
}

func newCEPositionStore(perOrg int, ttl time.Duration) *cePositionStore {
	return &cePositionStore{
		perOrg: perOrg,
		ttl:    ttl,
		clock:  time.Now,
		orgs:   map[string]map[string]*cePosition{},
	}
}

// keyHash keeps arbitrary source text out of the map keys' memory and logs.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

func (s *cePositionStore) get(orgID, key string) (ceCursor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.orgs[orgID]
	p, ok := m[keyHash(key)]
	if !ok {
		return ceCursor{}, false
	}
	now := s.clock()
	if now.Sub(p.lastUsed) > s.ttl {
		delete(m, keyHash(key))
		return ceCursor{}, false
	}
	p.lastUsed = now
	return p.cursor, true
}

func (s *cePositionStore) set(orgID, key string, pos ceCursor) {
	pos.V = ceCursorVersion
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	m := s.orgs[orgID]
	if m == nil {
		m = map[string]*cePosition{}
		s.orgs[orgID] = m
	}
	h := keyHash(key)
	if p, ok := m[h]; ok {
		p.cursor = pos
		p.lastUsed = now
		return
	}
	// Drop expired entries, then the least recently used one if the org
	// is still at its cap.
	for k, p := range m {
		if now.Sub(p.lastUsed) > s.ttl {
			delete(m, k)
		}
	}
	if len(m) >= s.perOrg {
		var oldestKey string
		var oldest time.Time
		for k, p := range m {
			if oldestKey == "" || p.lastUsed.Before(oldest) {
				oldestKey, oldest = k, p.lastUsed
			}
		}
		delete(m, oldestKey)
	}
	m[h] = &cePosition{cursor: pos, lastUsed: now}
}

func (s *cePositionStore) load(_ context.Context, orgID, key string) (ceCursor, bool) {
	return s.get(orgID, key)
}

func (s *cePositionStore) save(_ context.Context, orgID, key string, pos ceCursor) {
	s.set(orgID, key, pos)
}

// size returns the number of remembered positions for an org. Test helper.
func (s *cePositionStore) size(orgID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.orgs[orgID])
}

type ceSQLPositionStore struct {
	store     store.CloudEventPositionStore
	ttl       time.Duration
	perOrg    int
	mu        sync.Mutex
	lastPrune time.Time
	clock     func() time.Time
}

func newCESQLPositionStore(s store.CloudEventPositionStore, ttl time.Duration, perOrg int) *ceSQLPositionStore {
	return &ceSQLPositionStore{store: s, ttl: ttl, perOrg: perOrg, clock: time.Now}
}

func (s *ceSQLPositionStore) load(ctx context.Context, orgID, key string) (ceCursor, bool) {
	raw, ok, err := s.store.Get(ctx, orgID, keyHash(key))
	if err != nil {
		slog.Warn("cloudevents feed: load position failed", "org_id", orgID, "error", err)
		return ceCursor{}, false
	}
	if !ok {
		return ceCursor{}, false
	}
	c, err := decodeCECursor(raw)
	if err != nil || c == nil {
		return ceCursor{}, false
	}
	return *c, true
}

func (s *ceSQLPositionStore) save(ctx context.Context, orgID, key string, pos ceCursor) {
	s.prune(ctx)
	if err := s.store.Set(ctx, orgID, keyHash(key), pos.encode(), s.clock().UTC()); err != nil {
		slog.Warn("cloudevents feed: save position failed", "org_id", orgID, "error", err)
		return
	}
	if _, err := s.store.TrimOrg(ctx, orgID, s.perOrg); err != nil {
		slog.Warn("cloudevents feed: trim positions failed", "org_id", orgID, "error", err)
	}
}

func (s *ceSQLPositionStore) prune(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	if !s.lastPrune.IsZero() && now.Sub(s.lastPrune) < time.Hour {
		return
	}
	s.lastPrune = now
	if _, err := s.store.PruneBefore(ctx, now.Add(-s.ttl).UTC()); err != nil {
		slog.Warn("cloudevents feed: prune positions failed", "error", err)
	}
}

// cleanFilters trims each filter glob and drops empty/whitespace-only entries,
// returning the globs to OR-match against. Order is preserved; matching is
// order-insensitive so it doesn't matter.
func cleanFilters(filters []string) []string {
	cleaned := make([]string, 0, len(filters))
	for _, f := range filters {
		if f = strings.TrimSpace(f); f != "" {
			cleaned = append(cleaned, f)
		}
	}
	return cleaned
}
