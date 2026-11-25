package store

import "errors"

// ErrAlertNotFound is returned by AlertStore lookups that find no row. Callers
// use errors.Is to tell "nothing there" apart from a failed query.
var ErrAlertNotFound = errors.New("alert not found")

// ErrActiveAlertExists is returned by AlertStore.Create when the insert hit
// the one-active-alert-per-monitor unique index. Any other Create error is a
// real failure and must not be treated as a duplicate.
var ErrActiveAlertExists = errors.New("an active alert already exists for this monitor")

// ErrNoChecks is returned by CheckStore.GetLatest when a monitor has never
// been checked, so callers can tell a first check apart from a failed query.
var ErrNoChecks = errors.New("no checks found")

// ErrAlertStateChanged is returned by AlertStore.UpdateIfStatus when the alert
// exists but is no longer in one of the expected statuses, for example a
// stale tab acknowledging an alert that was already resolved (mon-10).
var ErrAlertStateChanged = errors.New("alert status changed")
