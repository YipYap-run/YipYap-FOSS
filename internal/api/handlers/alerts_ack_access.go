package handlers

import (
	"context"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// pagedEventTypes are the timeline events that name the user an alert was
// sent or routed to.
var pagedEventTypes = map[domain.AlertEventType]bool{
	domain.EventNotified:             true,
	domain.EventEscalated:            true,
	domain.EventOwnershipTransferred: true,
	domain.EventRouted:               true,
}

// alertPagedToUser reports whether the alert's timeline shows it was paged or
// routed to userID. Viewers may only acknowledge such alerts.
func alertPagedToUser(ctx context.Context, s store.Store, alertID, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	events, err := s.Alerts().ListEvents(ctx, alertID)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if e.TargetUserID == userID && pagedEventTypes[e.EventType] {
			return true, nil
		}
	}
	return false, nil
}
