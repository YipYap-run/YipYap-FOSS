package handlers

import (
	"errors"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/emailcfg"
)

// emailChannelType is the stored type of an email notification channel.
const emailChannelType = "smtp"

// SetPlatformEmail records whether this server has a platform mailer. Without
// one, email channels must bring their own SMTP server.
func (h *NotificationChannelHandler) SetPlatformEmail(available bool) {
	h.platformEmail = available
}

// emailConfigError is a 400-worthy problem with an email channel config.
type emailConfigError struct{ msg string }

func (e *emailConfigError) Error() string { return e.msg }

// checkEmailConfig validates an email channel config (already merged with
// stored secrets on update) and returns its canonical JSON. The same check
// runs on create, update and test so a channel that saves is a channel the
// notifier can send with, and a channel the notifier would refuse cannot be
// tested into looking healthy.
func (h *NotificationChannelHandler) checkEmailConfig(raw []byte) ([]byte, error) {
	cfg, err := emailcfg.Validate(raw, h.platformEmail)
	if err != nil {
		return nil, &emailConfigError{msg: "invalid email channel config: " + err.Error()}
	}
	if cfg.Mode == emailcfg.ModeSMTP {
		// The org's server is dialed from inside the platform network, so
		// the usual private-address rules apply (relaxed on self-hosted
		// builds via YIPYAP_ALLOW_PRIVATE_TARGETS).
		// The checker's own message talks about monitors, so the form gets
		// one written for a mail server instead.
		if err := checker.ValidateTarget(cfg.Host); err != nil {
			return nil, &emailConfigError{msg: "invalid email channel config: host must be a public mail server, not a private or internal address"}
		}
	}
	return cfg.Marshal(), nil
}

func writeEmailConfigError(w http.ResponseWriter, err error) {
	var ce *emailConfigError
	if errors.As(err, &ce) {
		errorResponse(w, http.StatusBadRequest, ce.msg)
		return
	}
	errorResponse(w, http.StatusInternalServerError, "failed to validate email channel config")
}

// Capabilities reports server-level facts the channel form needs: whether
// email channels can use the platform mailer, and the recipient cap.
func (h *NotificationChannelHandler) Capabilities(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"platform_email":       h.platformEmail,
		"email_max_recipients": emailcfg.MaxRecipients,
	})
}
