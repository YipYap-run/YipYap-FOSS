package handlers_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type jobCapture struct {
	mu   sync.Mutex
	jobs []domain.NotificationJob
}

func (c *jobCapture) handler(_ context.Context, _ string, data []byte) error {
	var job domain.NotificationJob
	if err := json.Unmarshal(data, &job); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs = append(c.jobs, job)
	return nil
}

func (c *jobCapture) snapshot() []domain.NotificationJob {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]domain.NotificationJob(nil), c.jobs...)
}

func (c *jobCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs = nil
}

func recordEmailPagedTarget(t *testing.T, s *sqlite.SQLiteStore, alert *domain.Alert, userID string) *domain.ContactMethod {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	m := &domain.ContactMethod{
		ID: uuid.New().String(), OrgID: alert.OrgID, UserID: userID, Type: domain.ContactEmail,
		Label: "Email", Config: `{"address":"resolved@test.com"}`,
		VerifiedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.ContactMethods().Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.Alerts().RecordPagedTarget(ctx, &domain.AlertPagedTarget{
		AlertID: alert.ID, TargetKey: "method:" + m.ID, Kind: domain.PagedTargetMethod,
		UserID: userID, MethodID: m.ID, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return m
}
