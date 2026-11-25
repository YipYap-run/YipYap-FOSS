package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// deadLetterConsumer is the durable consumer (and in-process queue group)
// that records dead-lettered jobs.
const deadLetterConsumer = "delivery-recorder"

// DeadLetter is the notify.dead payload. It carries what the recorder
// needs and nothing more: no target config, no message body, and only the
// sanitized failure reason (see PublicDeliveryError), so a secret in a
// provider error never reaches the stream or the database.
type DeadLetter struct {
	JobID        string    `json:"job_id"`
	AlertID      string    `json:"alert_id,omitempty"`
	OrgID        string    `json:"org_id"`
	ChannelID    string    `json:"channel_id,omitempty"`
	ChannelName  string    `json:"channel_name,omitempty"`
	ChannelType  string    `json:"channel_type"`
	TargetUserID string    `json:"target_user_id,omitempty"`
	Reason       string    `json:"reason"`
	FailedAt     time.Time `json:"failed_at"`
}

// NewDeadLetter builds the notify.dead payload for a job whose retries are
// exhausted. err is reduced to its public failure class.
func NewDeadLetter(job domain.NotificationJob, err error, at time.Time) DeadLetter {
	return DeadLetter{
		JobID:        job.ID,
		AlertID:      job.AlertID,
		OrgID:        job.OrgID,
		ChannelID:    job.ChannelID,
		ChannelName:  job.ChannelName,
		ChannelType:  job.Channel,
		TargetUserID: job.TargetUserID,
		Reason:       PublicDeliveryError(err),
		FailedAt:     at.UTC(),
	}
}

// legacyDeadLetter is the notify.dead shape published before DeadLetter.
// Messages in that shape can still sit in the stream after an upgrade.
type legacyDeadLetter struct {
	Job   *domain.NotificationJob `json:"job"`
	Error string                  `json:"error"`
}

func decodeDeadLetter(data []byte) (DeadLetter, error) {
	var dl DeadLetter
	if err := json.Unmarshal(data, &dl); err != nil {
		return DeadLetter{}, err
	}
	if dl.JobID != "" {
		return dl, nil
	}
	var legacy legacyDeadLetter
	if err := json.Unmarshal(data, &legacy); err != nil || legacy.Job == nil || legacy.Job.ID == "" {
		return DeadLetter{}, errors.New("notify.dead: no job id")
	}
	// The legacy raw error text is not trusted: it can hold provider
	// response bodies or URLs with tokens. Record the generic class only.
	return NewDeadLetter(*legacy.Job, errors.New("legacy dead letter"), time.Now()), nil
}

// OutcomeRecorder records final delivery outcomes. The dispatcher calls
// RecordSent after a provider accepts a job. Failures arrive through the
// notify.dead consumer (StartDeadLetterConsumer).
type OutcomeRecorder interface {
	RecordSent(ctx context.Context, job domain.NotificationJob) error
}

// Recorder writes delivery outcomes to the alert timeline and to the
// channel's health columns.
type Recorder struct {
	store  store.Store
	now    func() time.Time
	alerts *ChannelAlerts
}

// RecorderOption configures a Recorder.
type RecorderOption func(*Recorder)

// WithChannelAlerts emails org owners when a channel keeps failing and
// when it recovers. A nil a turns the emails off.
func WithChannelAlerts(a *ChannelAlerts) RecorderOption {
	return func(r *Recorder) { r.alerts = a }
}

// NewRecorder returns a Recorder backed by s.
func NewRecorder(s store.Store, opts ...RecorderOption) *Recorder {
	r := &Recorder{store: s, now: func() time.Time { return time.Now().UTC() }}
	for _, o := range opts {
		o(r)
	}
	return r
}

// outcomeEventNamespace derives stable alert event IDs from job IDs, so a
// redelivered outcome does not add a second timeline row or count twice.
var outcomeEventNamespace = uuid.MustParse("8f8b0b8e-2d55-4d0e-9f53-6f0c3c1c7a11")

func outcomeEventID(kind domain.AlertEventType, jobID string) string {
	return uuid.NewSHA1(outcomeEventNamespace, []byte(string(kind)+":"+jobID)).String()
}

type outcomeDetail struct {
	JobID       string `json:"job_id"`
	ChannelID   string `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// RecordSent writes a notification_sent timeline event and marks the
// channel healthy. If the owners were emailed that the channel was
// failing, they get a note that it is delivering again.
func (r *Recorder) RecordSent(ctx context.Context, job domain.NotificationJob) error {
	if job.ID == "" {
		return nil
	}
	at := r.now()
	detail, _ := json.Marshal(outcomeDetail{JobID: job.ID, ChannelID: job.ChannelID, ChannelName: job.ChannelName})
	var recovered bool
	err := r.store.Tx(ctx, func(tx store.Store) error {
		first, err := r.writeEvent(ctx, tx, job.AlertID, &domain.AlertEvent{
			ID:           outcomeEventID(domain.EventNotificationSent, job.ID),
			AlertID:      job.AlertID,
			EventType:    domain.EventNotificationSent,
			Channel:      job.Channel,
			TargetUserID: job.TargetUserID,
			Detail:       detail,
			CreatedAt:    at,
		})
		if err != nil || !first || job.ChannelID == "" {
			return err
		}
		recovered, err = tx.NotificationChannels().RecordDeliverySuccess(ctx, job.ChannelID, at)
		return err
	})
	if err == nil && recovered {
		r.alerts.ChannelRecovered(ctx, job.ChannelID)
	}
	return err
}

// RecordFailed writes a notification_failed timeline event and bumps the
// channel's consecutive failure count. It returns the new count (0 when
// the job has no channel or the outcome was already recorded).
func (r *Recorder) RecordFailed(ctx context.Context, dl DeadLetter) (int, error) {
	if dl.JobID == "" {
		return 0, nil
	}
	at := dl.FailedAt
	if at.IsZero() {
		at = r.now()
	}
	reason := dl.Reason
	if reason == "" {
		reason = PublicDeliveryError(errors.New("unknown"))
	}
	detail, _ := json.Marshal(outcomeDetail{JobID: dl.JobID, ChannelID: dl.ChannelID, ChannelName: dl.ChannelName, Reason: reason})
	var count int
	err := r.store.Tx(ctx, func(tx store.Store) error {
		first, err := r.writeEvent(ctx, tx, dl.AlertID, &domain.AlertEvent{
			ID:           outcomeEventID(domain.EventNotificationFailed, dl.JobID),
			AlertID:      dl.AlertID,
			EventType:    domain.EventNotificationFailed,
			Channel:      dl.ChannelType,
			TargetUserID: dl.TargetUserID,
			Detail:       detail,
			CreatedAt:    at,
		})
		if err != nil || !first || dl.ChannelID == "" {
			return err
		}
		count, err = tx.NotificationChannels().RecordDeliveryFailure(ctx, dl.ChannelID, at, reason)
		return err
	})
	if err == nil && count > 0 {
		// After the commit, so the claim sees this failure in the count.
		r.alerts.ChannelFailed(ctx, dl.ChannelID, count)
	}
	return count, err
}

// writeEvent inserts the outcome event once. Jobs without an alert (such
// as CloudEvent deliveries) have no timeline; they report first=true so
// the channel health is still updated.
func (r *Recorder) writeEvent(ctx context.Context, tx store.Store, alertID string, evt *domain.AlertEvent) (bool, error) {
	if alertID == "" {
		return true, nil
	}
	first, err := tx.Alerts().CreateEventOnce(ctx, evt)
	if err != nil {
		return false, fmt.Errorf("record %s event: %w", evt.EventType, err)
	}
	return first, nil
}

// StartDeadLetterConsumer subscribes rec to notify.dead. On JetStream it
// is a durable pull consumer shared by every replica that runs it; on the
// in-process bus it is a queue subscription, so the same code records
// failures in single-box mode.
func StartDeadLetterConsumer(b bus.Bus, rec *Recorder) error {
	if b == nil || rec == nil {
		return nil
	}
	return b.PullSubscribe(subjectNotifyDead, deadLetterConsumer, func(ctx context.Context, msg *bus.Msg) error {
		dl, err := decodeDeadLetter(msg.Data)
		if err != nil {
			slog.Error("notify.dead: undecodable message dropped", "error", err)
			return msg.Ack()
		}
		if _, err := rec.RecordFailed(ctx, dl); err != nil {
			// Returning the error naks the message for redelivery.
			slog.Error("notify.dead: record failure", "job_id", dl.JobID, "channel_id", dl.ChannelID, "error", err)
			return err
		}
		return msg.Ack()
	})
}
