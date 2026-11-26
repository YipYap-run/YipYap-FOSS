package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// A contact_method link code adds the caller's own Telegram contact method
// on the platform bot. The user makes the code while signed in and sends
// it to the bot from their own private chat, which proves they hold both
// ends, so the method is created verified.

const telegramContactPrivateOnlyReply = "This code connects your own Telegram account, so it only works in a private chat with this bot. " +
	"It was sent where others can see it, so it no longer works. Create a new one in YipYap and send it to the bot directly."

// prepareContactLinkCode checks a contact_method code request. Any member
// may link their own contact method; the same plan gate and per-user limit
// as creating a contact method apply.
func (h *NotificationChannelHandler) prepareContactLinkCode(w http.ResponseWriter, r *http.Request) bool {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.UserID == "" || claims.Role == "api_key" {
		errorResponse(w, http.StatusForbidden, "contact methods can only be managed by a signed-in user")
		return false
	}
	cp, ok := h.store.(store.ContactMethodProvider)
	if !ok {
		errorResponse(w, http.StatusNotImplemented, "contact methods are not supported by this store")
		return false
	}
	if err := contactmethod.PlanError(r.Context(), h.store, claims.OrgID, domain.ContactTelegram); err != nil {
		planDenied(w, err)
		return false
	}
	n, err := cp.ContactMethods().CountByUser(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create link code")
		return false
	}
	if n >= contactmethod.MaxMethodsPerUser {
		errorResponse(w, http.StatusConflict, fmt.Sprintf("you can keep at most %d contact methods", contactmethod.MaxMethodsPerUser))
		return false
	}
	return true
}

// linkContactMethod finishes a claimed contact_method code: it adds (or
// verifies) the user's Telegram contact method for chat and returns the
// reply for the chat.
func (l *telegramLinker) linkContactMethod(ctx context.Context, codes store.TelegramLinkCodeStore, row *domain.TelegramLinkCode, chat telegramChat, orgName string) string {
	m, existed, msg := l.bindContactMethod(ctx, row, chat)
	if m == nil {
		return msg
	}
	if err := codes.SetLinked(ctx, row.ID, strconv.FormatInt(chat.ID, 10), chat.displayTitle(), m.ID); err != nil {
		slog.Error("telegram link: record result", "code_id", row.ID, "error", err)
	}
	slog.Info("telegram link: contact method linked", "org_id", row.OrgID, "user_id", row.SubjectID, "contact_method_id", m.ID)
	rules := "\n\nTo get pages here, open YipYap, go to Settings, Contact Methods, and add a notification rule that uses this method."
	if existed {
		return fmt.Sprintf("This Telegram account is linked to your YipYap account in %s.", orgName) + rules
	}
	return fmt.Sprintf("Connected. This Telegram account is now a contact method on your YipYap account in %s.", orgName) + rules
}

// bindContactMethod returns the user's verified platform bot Telegram
// method for chat, creating it when needed. existed is true when the user
// already had one for this chat. On failure the method is nil and msg is
// the reply.
func (l *telegramLinker) bindContactMethod(ctx context.Context, row *domain.TelegramLinkCode, chat telegramChat) (m *domain.ContactMethod, existed bool, msg string) {
	const saveFailed = "YipYap could not save the link. Create a new code and try again."
	cp, ok := l.store.(store.ContactMethodProvider)
	if !ok {
		return nil, false, "Linking is not available on this server."
	}
	gone := "The YipYap account this code was made for no longer exists. Create a new code in YipYap."
	u, err := l.store.Users().GetByID(ctx, row.SubjectID)
	if err != nil || u == nil || u.OrgID != row.OrgID || row.CreatedBy != row.SubjectID {
		return nil, false, gone
	}
	if err := contactmethod.PlanError(ctx, l.store, row.OrgID, domain.ContactTelegram); err != nil {
		var pe *contactmethod.PlanRequiredError
		if errors.As(err, &pe) {
			return nil, false, pe.Msg + ", so no contact method was added."
		}
		return nil, false, saveFailed
	}

	methods := cp.ContactMethods()
	chatID := strconv.FormatInt(chat.ID, 10)
	now := l.now().UTC()
	list, err := methods.ListByUser(ctx, u.ID)
	if err != nil {
		slog.Error("telegram link: list contact methods", "user_id", u.ID, "error", err)
		return nil, false, saveFailed
	}
	if found := platformTelegramMethod(list, row.OrgID, chatID); found != nil {
		if !found.Verified() {
			if err := methods.MarkVerified(ctx, u.ID, found.ID, now); err != nil {
				slog.Error("telegram link: verify contact method", "contact_method_id", found.ID, "error", err)
				return nil, false, saveFailed
			}
			found.VerifiedAt = &now
		}
		return found, true, ""
	}
	if len(list) >= contactmethod.MaxMethodsPerUser {
		return nil, false, fmt.Sprintf("Your YipYap account already has %d contact methods, the most allowed, so no contact method was added. Remove one and create a new code.", contactmethod.MaxMethodsPerUser)
	}

	raw, _ := json.Marshal(map[string]string{"chat_id": chatID})
	cfg, err := contactmethod.Normalize(ctx, l.store, row.OrgID, domain.ContactTelegram, raw, "")
	if err != nil {
		slog.Error("telegram link: contact method config", "error", err)
		return nil, false, saveFailed
	}
	m = &domain.ContactMethod{
		ID: uuid.New().String(), OrgID: row.OrgID, UserID: u.ID,
		Type: domain.ContactTelegram, Label: telegramContactLabel(chat), Config: cfg,
		VerifiedAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if err := methods.Create(ctx, m); err != nil {
		slog.Error("telegram link: create contact method", "user_id", u.ID, "error", err)
		return nil, false, saveFailed
	}
	return m, false, ""
}

// platformTelegramMethod finds a Telegram method in orgID that sends to
// chatID through the platform bot.
func platformTelegramMethod(list []*domain.ContactMethod, orgID, chatID string) *domain.ContactMethod {
	for _, m := range list {
		if m.Type != domain.ContactTelegram || m.OrgID != orgID {
			continue
		}
		var c struct {
			ChatID       string `json:"chat_id"`
			ViaChannelID string `json:"via_channel_id"`
		}
		if json.Unmarshal([]byte(m.Config), &c) != nil {
			continue
		}
		if c.ViaChannelID == "" && strings.TrimSpace(c.ChatID) == chatID {
			return m
		}
	}
	return nil
}

// telegramContactLabel names a linked method after the Telegram username,
// so a person with two accounts can tell them apart.
func telegramContactLabel(chat telegramChat) string {
	if u := strings.TrimSpace(chat.Username); u != "" && len([]rune(u)) <= contactmethod.MaxLabelLength-len("Telegram (@)") {
		return "Telegram (@" + u + ")"
	}
	return defaultLabel(domain.ContactTelegram)
}
