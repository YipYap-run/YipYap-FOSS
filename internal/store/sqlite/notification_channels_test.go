package sqlite

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestNotificationChannelCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	ch := &domain.NotificationChannel{
		ID:      uuid.New().String(),
		OrgID:   org.ID,
		Type:    "slack",
		Name:    "#alerts",
		Config:  `{"webhook_url":"https://hooks.slack.com/xxx"}`,
		Enabled: true,
	}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}

	got, err := s.NotificationChannels().GetByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != "slack" || got.Name != "#alerts" || !got.Enabled {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestNotificationChannelListByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	for _, name := range []string{"slack", "email"} {
		ch := &domain.NotificationChannel{
			ID:      uuid.New().String(),
			OrgID:   org.ID,
			Type:    name,
			Name:    name,
			Enabled: true,
		}
		if err := s.NotificationChannels().Create(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}

	channels, err := s.NotificationChannels().ListByOrg(ctx, org.ID, store.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 {
		t.Fatalf("expected 2, got %d", len(channels))
	}
}
