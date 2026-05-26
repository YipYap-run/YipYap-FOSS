// Package handlers implements the Phase-2 reply handlers that turn inbound
// CloudEvent replies into read-only timeline annotations on the referenced
// alert.
//
// Each handler:
//
//   - Unmarshals the reply's data payload into its typed schema.
//   - Validates the reply's alert_id against the outbound registry entry
//     (cross-alert hijack defence).
//   - Performs type-specific validation (attachment shape, enum kinds, etc).
//   - Writes a single domain.AlertEvent row via store.Alerts().CreateEvent
//     with a JSON-encoded payload captured in the event's Detail column.
//
// The handlers never mutate alert state (severity, ack status, etc.),
// Phase 5 will introduce mutating handlers behind a separate opt-in. The
// verified OIDC `sub` claim is persisted alongside the payload so future
// audit queries can trace which identity produced a given annotation.
package handlers
