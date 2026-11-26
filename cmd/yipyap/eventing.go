package main

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/events"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// eventing is empty in this build: domain events are not published to
// external sinks.
type eventing struct{}

func newEventing(_ *config.Config, _ bus.Bus) *eventing { return &eventing{} }

// Emitter returns nil, which turns event emission into a no-op.
func (*eventing) Emitter() events.Emitter { return nil }

func (*eventing) RegisterDelivery(_ *notify.Dispatcher, _ store.Store, _ notify.DecryptFunc, _ *crypto.Envelope) {
}

func (*eventing) StartRouter(_ context.Context, _ store.Store, _ *crypto.Envelope) error { return nil }

func (*eventing) Close() {}
