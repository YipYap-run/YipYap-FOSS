package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type recordedRecovery struct {
	mu  sync.Mutex
	ids []string
}

func (r *recordedRecovery) ChannelRecovered(_ context.Context, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id)
}

func (r *recordedRecovery) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids)
}

// A Test that ends a failure streak the owners were emailed about sends
// them the "delivering again" follow-up once, and says so in the response
// so the SPA can tell the person who ran the test.
func TestChannelTest_TellsOwnersWhenItEndsANotifiedStreak(t *testing.T) {
	ctx := context.Background()
	rec := &recordedRecovery{}
	s, ch, test := recoveryFixture(t, rec)

	// Failing, but below the point where owners were emailed: no follow-up.
	if _, err := s.NotificationChannels().RecordDeliveryFailure(ctx, ch.ID, time.Now().UTC(), "Delivery failed."); err != nil {
		t.Fatal(err)
	}
	if test() || rec.count() != 0 {
		t.Fatalf("follow-up sent for a streak nobody was told about (%d)", rec.count())
	}

	// Owners were emailed about this streak: the fixing Test tells them.
	notifyStreak(t, s, ch.ID, 3)
	if !test() || rec.count() != 1 || rec.ids[0] != ch.ID {
		t.Fatalf("owners_notified false or follow-up count %d, want 1", rec.count())
	}

	// A second Test is not news.
	if test() || rec.count() != 1 {
		t.Fatalf("second test sent another follow-up (%d)", rec.count())
	}
}

// Without a mailer (a nil *notify.ChannelAlerts) nobody is emailed, so
// the response must not claim the owners were told.
func TestChannelTest_NoMailerDoesNotClaimOwnersNotified(t *testing.T) {
	var none *notify.ChannelAlerts
	s, ch, test := recoveryFixture(t, none)
	notifyStreak(t, s, ch.ID, 3)
	if test() {
		t.Fatal("owners_notified true with no mailer")
	}
}

// recoveryFixture returns a store with one webhook channel and a func that
// runs Test on it and reports owners_notified.
func recoveryFixture(t *testing.T, notifier handlers.ChannelRecoveryNotifier) (*sqlite.SQLiteStore, *domain.NotificationChannel, func() bool) {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	org := &domain.Org{ID: uuid.NewString(), Name: "Acme", Slug: "acme-" + uuid.NewString()[:8], Plan: domain.PlanFree}
	if err := s.Orgs().Create(ctx, org); err != nil {
		t.Fatal(err)
	}
	ch := &domain.NotificationChannel{ID: uuid.NewString(), OrgID: org.ID, Type: "webhook", Name: "hook", Config: `{"url":"https://example.com/h"}`, Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}

	h := handlers.NewNotificationChannelHandler(s, &capturingTestSender{})
	h.SetChannelAlerts(notifier)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			c := &auth.Claims{UserID: uuid.NewString(), OrgID: org.ID, Role: string(domain.RoleMember)}
			next.ServeHTTP(w, req.WithContext(middleware.SetClaims(req.Context(), c)))
		})
	})
	r.Post("/notification-channels/{id}/test", h.Test)

	test := func() bool {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/notification-channels/"+ch.ID+"/test", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("test: status %d %s", w.Code, w.Body.String())
		}
		var out struct {
			OwnersNotified bool `json:"owners_notified"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.OwnersNotified
	}

	return s, ch, test
}

// notifyStreak records n failures and claims the owner email for them.
func notifyStreak(t *testing.T, s *sqlite.SQLiteStore, chID string, n int) {
	t.Helper()
	ctx := context.Background()
	for range n {
		if _, err := s.NotificationChannels().RecordDeliveryFailure(ctx, chID, time.Now().UTC(), "Delivery failed."); err != nil {
			t.Fatal(err)
		}
	}
	won, err := s.NotificationChannels().ClaimFailureNotice(ctx, chID, n, time.Now().UTC().Truncate(time.Second))
	if err != nil || !won {
		t.Fatalf("claim: %v %v", won, err)
	}
}
