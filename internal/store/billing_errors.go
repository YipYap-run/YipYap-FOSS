package store

import "errors"

// ErrBillingNotFound is returned by BillingStore.GetByOrgID when the org
// has no billing row (free orgs, and promo or ops-assigned plans that
// never went through checkout). Callers use errors.Is to tell it apart
// from a failed lookup.
var ErrBillingNotFound = errors.New("not found")
