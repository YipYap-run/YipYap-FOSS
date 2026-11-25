package store

import "errors"

// ErrOIDCDomainClaimed is returned when a domain is already verified by
// another org, so it cannot be added or verified again.
var ErrOIDCDomainClaimed = errors.New("oidc domain already verified by another organization")

// ErrOIDCDomainNotFound is returned when a domain row does not exist.
var ErrOIDCDomainNotFound = errors.New("oidc domain not found")
