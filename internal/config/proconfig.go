package config

import (
	"log"
	"os"
	"strconv"
)

// ProConfig contains the DB driver (hardcoded to sqlite in FOSS).
// Checker tuning fields are present but always zero  - defaults apply.
type ProConfig struct {
	DBDriver      string
	PublicBaseURL string
	OpsToken      string

	// TwilioAuthToken is SaaS-only and always empty in FOSS. Present so
	// shared wiring (ServerOptions) can reference it in both builds.
	TwilioAuthToken string
	// TelnyxWebhookSecret is SaaS-only and always empty in FOSS. Present so
	// shared wiring (ServerOptions) can reference it in both builds.
	TelnyxWebhookSecret string

	// SlackClientID and SlackClientSecret configure the hosted Slack app
	// and are always empty in FOSS. Present so shared wiring can reference
	// them in both builds.
	SlackClientID     string
	SlackClientSecret string

	// DiscordClientID, DiscordClientSecret and DiscordBotToken configure the
	// hosted Discord app and are always empty in FOSS.
	DiscordClientID     string
	DiscordClientSecret string
	DiscordBotToken     string

	CheckerWorkers           int
	CheckerChannelSize       int
	CheckerPriorityThreshold int
	CheckerBatchSize         int
	CheckerBatchWriters      int
	CheckerFlushConcurrency  int

	// Transactional email (password resets, etc.)
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string

	AllowPrivateTargets bool // FOSS defaults to true so self-hosters can monitor their own networks

	// AllowPrivateControlPlaneTargets governs SSRF protection for
	// control-plane URLs (CloudEvent sink_url, OIDC token_url / issuer,
	// JWKS). Distinct from AllowPrivateTargets: a FOSS deployment that
	// legitimately watches intranet monitors should NOT simultaneously
	// accept attacker-submitted sink_url pointing at the same intranet.
	// Defaults to false even in FOSS; override via
	// YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS=true for localhost
	// CloudEvent development.
	AllowPrivateControlPlaneTargets bool

	// Optional operator caps. FOSS has no plan limits; 0 (unset) means no
	// cap. MinCheckIntervalSeconds 0 means the built-in technical floor.
	MaxMonitorsPerOrg             int // YIPYAP_MAX_MONITORS_PER_ORG
	MaxNotificationChannelsPerOrg int // YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG
	MinCheckIntervalSeconds       int // YIPYAP_MIN_CHECK_INTERVAL_SECONDS
}

func loadProConfig(c *Config) {
	c.DBDriver = "sqlite"
	// Public URL of this instance, used for acknowledge links in
	// notifications, CORS, and the WebSocket origin check.
	c.PublicBaseURL = envOr("YIPYAP_PUBLIC_BASE_URL", "")
	// FOSS/self-hosted: allow monitoring private networks by default.
	c.AllowPrivateTargets = envBool("YIPYAP_ALLOW_PRIVATE_TARGETS", true)
	// Control-plane URLs (sink, OIDC) default-deny even in FOSS.
	c.AllowPrivateControlPlaneTargets = envBool("YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS", false)

	c.MaxMonitorsPerOrg = envNonNegInt("YIPYAP_MAX_MONITORS_PER_ORG")
	c.MaxNotificationChannelsPerOrg = envNonNegInt("YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG")
	c.MinCheckIntervalSeconds = envNonNegInt("YIPYAP_MIN_CHECK_INTERVAL_SECONDS")
}

// envNonNegInt reads an optional cap. Unset, malformed or negative values
// mean 0 (no cap); a bad value is logged so the operator can see it was
// ignored.
func envNonNegInt(key string) int {
	v := os.Getenv(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		log.Printf("config: %s=%q is not a non-negative integer; ignoring it", key, v)
		return 0
	}
	return n
}
