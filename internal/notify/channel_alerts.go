package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Mailer sends a plain-text email. *mailer.Mailer satisfies it.
type Mailer interface {
	Send(to, subject, body string) error
}

// ChannelAlerts emails org owners when a notification channel fails
// several deliveries in a row, and again when it delivers after that.
// The email for a failure streak is claimed in the store, so replicas that
// record failures for the same channel at the same time send it once.
type ChannelAlerts struct {
	store   store.Store
	mail    Mailer
	baseURL string
	now     func() time.Time
	// goAsync runs the recovery email off the delivery path. Tests swap
	// it for a synchronous call.
	goAsync func(func())
}

// NewChannelAlerts returns nil when m is nil (including a typed nil such
// as the *mailer.Mailer returned when SMTP is not configured). A nil
// *ChannelAlerts is a valid no-op.
func NewChannelAlerts(s store.Store, m Mailer, baseURL string) *ChannelAlerts {
	if s == nil || isNilMailer(m) {
		return nil
	}
	return &ChannelAlerts{
		store:   s,
		mail:    m,
		baseURL: strings.TrimRight(baseURL, "/"),
		now:     func() time.Time { return time.Now().UTC() },
		goAsync: func(f func()) { go f() },
	}
}

func isNilMailer(m Mailer) bool {
	if m == nil {
		return true
	}
	v := reflect.ValueOf(m)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return v.IsNil()
	}
	return false
}

// channelAlertSendTimeout bounds one owner email batch.
const channelAlertSendTimeout = 2 * time.Minute

// ChannelFailed is called after a failed delivery was recorded and the
// channel's consecutive failure count is now count. It emails the org
// owners once the count reaches the org's threshold, once per streak.
// Errors are logged, not returned: the failure itself is already recorded.
func (a *ChannelAlerts) ChannelFailed(ctx context.Context, channelID string, count int) {
	if a == nil || channelID == "" || count < domain.MinChannelFailureAlertThreshold {
		return
	}
	ch, err := a.store.NotificationChannels().GetByID(ctx, channelID)
	if err != nil {
		slog.Warn("channel failure alert: load channel", "channel_id", channelID, "error", err)
		return
	}
	threshold := a.threshold(ctx, ch.OrgID)
	if count < threshold || ch.FailureNotifiedAt != nil {
		return
	}
	// Second precision so the claim time compares equal on release in
	// every store (SQLite keeps RFC 3339 seconds).
	claimAt := a.now().Truncate(time.Second)
	won, err := a.store.NotificationChannels().ClaimFailureNotice(ctx, ch.ID, threshold, claimAt)
	if err != nil {
		slog.Warn("channel failure alert: claim", "channel_id", ch.ID, "error", err)
		return
	}
	if !won {
		return
	}
	subject, body := a.failureEmail(ctx, ch, threshold)
	if sent := a.sendToOwners(ctx, ch.OrgID, subject, body); sent == 0 {
		// Nobody got it. Give the claim back so the next failure in this
		// streak tries again instead of staying silent.
		if err := a.store.NotificationChannels().ReleaseFailureNotice(ctx, ch.ID, claimAt); err != nil {
			slog.Warn("channel failure alert: release claim", "channel_id", ch.ID, "error", err)
		}
	}
}

// ChannelRecovered emails the org owners that a channel they were told
// about is delivering again. The store already decided that this caller
// is the one to send it. The email goes out in the background so a slow
// mail server does not hold up a notification worker.
func (a *ChannelAlerts) ChannelRecovered(ctx context.Context, channelID string) {
	if a == nil || channelID == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	a.goAsync(func() {
		ch, err := a.store.NotificationChannels().GetByID(ctx, channelID)
		if err != nil {
			slog.Warn("channel recovery alert: load channel", "channel_id", channelID, "error", err)
			return
		}
		subject, body := a.recoveryEmail(ctx, ch)
		a.sendToOwners(ctx, ch.OrgID, subject, body)
	})
}

func (a *ChannelAlerts) threshold(ctx context.Context, orgID string) int {
	sp, ok := a.store.(store.OrgSettingsProvider)
	if !ok {
		return domain.DefaultChannelFailureAlertThreshold
	}
	all, err := sp.OrgSettings().GetAll(ctx, orgID)
	if err != nil {
		slog.Warn("channel failure alert: load org settings", "org_id", orgID, "error", err)
		return domain.DefaultChannelFailureAlertThreshold
	}
	return domain.ChannelFailureAlertThresholdFromSettings(all)
}

// ownerEmails returns the addresses of the org's active owners.
func ownerEmails(ctx context.Context, s store.Store, orgID string) ([]string, error) {
	var out []string
	const page = 200
	for offset := 0; ; offset += page {
		users, err := s.Users().ListByOrg(ctx, orgID, store.ListParams{Limit: page, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			if u.Role == domain.RoleOwner && u.DisabledAt == nil && strings.TrimSpace(u.Email) != "" {
				out = append(out, u.Email)
			}
		}
		if len(users) < page {
			return out, nil
		}
	}
}

// sendToOwners emails every owner separately (one owner's address never
// appears in another's To header) and returns how many sends succeeded.
func (a *ChannelAlerts) sendToOwners(ctx context.Context, orgID, subject, body string) int {
	return sendToOrgOwners(ctx, a.store, a.mail, orgID, subject, body)
}

// sendToOrgOwners emails every active owner of orgID separately and
// returns how many sends succeeded.
func sendToOrgOwners(ctx context.Context, s store.Store, m Mailer, orgID, subject, body string) int {
	ctx, cancel := context.WithTimeout(ctx, channelAlertSendTimeout)
	defer cancel()
	owners, err := ownerEmails(ctx, s, orgID)
	if err != nil {
		slog.Warn("owner email: list owners", "org_id", orgID, "error", err)
		return 0
	}
	if len(owners) == 0 {
		slog.Warn("owner email: org has no active owner to email", "org_id", orgID)
		return 0
	}
	sent := 0
	for _, to := range owners {
		if ctx.Err() != nil {
			break
		}
		if err := m.Send(to, subject, body); err != nil {
			slog.Warn("owner email: send", "org_id", orgID, "error", err)
			continue
		}
		sent++
	}
	return sent
}

func (a *ChannelAlerts) orgName(ctx context.Context, orgID string) string {
	org, err := a.store.Orgs().GetByID(ctx, orgID)
	if err != nil || org == nil {
		return ""
	}
	return org.Name
}

// channelsURL is the SPA page that lists channels with their health and
// a Test button. The channel query parameter makes the page scroll to and
// highlight that channel.
func (a *ChannelAlerts) channelsURL(channelID string) string {
	if a.baseURL == "" {
		return ""
	}
	u := a.baseURL + "/settings/channels"
	if channelID != "" {
		u += "?channel=" + url.QueryEscape(channelID)
	}
	return u
}

// maxSubjectName caps a channel name in a subject line.
const maxSubjectName = 80

func subjectName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > maxSubjectName {
		name = string(r[:maxSubjectName-3]) + "..."
	}
	return name
}

func channelLabel(ch *domain.NotificationChannel) string {
	name := strings.Join(strings.Fields(ch.Name), " ")
	if name == "" {
		name = "(unnamed)"
	}
	return fmt.Sprintf("%q (%s)", name, ch.Type)
}

func (a *ChannelAlerts) failureEmail(ctx context.Context, ch *domain.NotificationChannel, threshold int) (string, string) {
	subject := fmt.Sprintf("Notification channel %q is failing", subjectName(ch.Name))
	var b strings.Builder
	where := ""
	if org := a.orgName(ctx, ch.OrgID); org != "" {
		where = fmt.Sprintf(" in %s", org)
	}
	n := ch.ConsecutiveFailures
	if n < threshold {
		n = threshold
	}
	fmt.Fprintf(&b, "The notification channel %s%s has failed its last %d deliveries in a row.\n\n", channelLabel(ch), where, n)
	if ch.LastError != "" {
		fmt.Fprintf(&b, "Last error: %s\n", ch.LastError)
	}
	if ch.LastFailureAt != nil {
		fmt.Fprintf(&b, "Last failure: %s\n", ch.LastFailureAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	b.WriteString("\nAlerts routed to this channel may not reach anyone until it is fixed. ")
	if u := a.channelsURL(ch.ID); u != "" {
		fmt.Fprintf(&b, "Check the channel settings and send a test:\n%s\n", u)
	} else {
		b.WriteString("Open Settings, then Channels, in YipYap to check the channel and send a test.\n")
	}
	b.WriteString("\nYou will not get another email about this channel until it delivers again and then starts failing.\n")
	b.WriteString("You get this email because you are an owner of the organization. Owners and admins can change how many failures trigger it under Settings, then Organization.\n")
	return subject, b.String()
}

func (a *ChannelAlerts) recoveryEmail(ctx context.Context, ch *domain.NotificationChannel) (string, string) {
	subject := fmt.Sprintf("Notification channel %q is delivering again", subjectName(ch.Name))
	var b strings.Builder
	where := ""
	if org := a.orgName(ctx, ch.OrgID); org != "" {
		where = fmt.Sprintf(" in %s", org)
	}
	fmt.Fprintf(&b, "The notification channel %s%s delivered a notification successfully", channelLabel(ch), where)
	if ch.LastSuccessAt != nil {
		fmt.Fprintf(&b, " at %s", ch.LastSuccessAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	b.WriteString(" after the failures we emailed you about.\n")
	if u := a.channelsURL(ch.ID); u != "" {
		fmt.Fprintf(&b, "\nChannel settings: %s\n", u)
	}
	return subject, b.String()
}
