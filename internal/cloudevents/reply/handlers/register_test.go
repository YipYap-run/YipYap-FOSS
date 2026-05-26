package handlers

import (
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
)

func TestRegisterAll_RegistersAllHandlers(t *testing.T) {
	s := newTestStore(t)
	reg := reply.NewMemoryRegistry(10, time.Hour)
	d := reply.NewDispatcher(reg)

	hs := RegisterAll(d, s)
	if len(hs) != 13 {
		t.Fatalf("returned %d handlers, want 13 (3 Phase-2 + 10 Phase-5)", len(hs))
	}

	want := map[string]bool{
		// Phase-2.
		types.TypeReplyAlertClaimedV1:  false,
		types.TypeReplyAlertEnrichedV1: false,
		types.TypeReplyAlertLinkedV1:   false,
		// Phase-5.
		types.TypeReplyAlertSuppressedV1:          false,
		types.TypeReplyAlertDeduplicatedV1:        false,
		types.TypeReplyAlertEscalatedV1:           false,
		types.TypeReplyAlertRemediationStartedV1:  false,
		types.TypeReplyAlertRemediationResultV1:   false,
		types.TypeReplyAlertOwnershipV1:           false,
		types.TypeReplyAlertAckWithContextV1:      false,
		types.TypeReplyAlertRouteV1:               false,
		types.TypeReplyAlertStatusPageV1:          false,
		types.TypeReplyMonitorDeregisterV1:        false,
	}
	for _, h := range hs {
		if _, ok := want[h.Type()]; !ok {
			t.Errorf("unexpected handler type %q", h.Type())
		} else {
			want[h.Type()] = true
		}
	}
	for typ, seen := range want {
		if !seen {
			t.Errorf("handler for %q was not returned", typ)
		}
	}
}

func TestRegisterAll_NilSafety(t *testing.T) {
	if hs := RegisterAll(nil, nil); hs != nil {
		t.Errorf("expected nil slice when both args nil, got %d", len(hs))
	}
}
