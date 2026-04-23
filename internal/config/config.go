package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr         string
	DBDsn              string // connection string  - file path for sqlite, DSN for postgres/mariadb
	JWTSecret          string
	JWTExpiry          time.Duration
	APIKeySecret       string // separate HMAC key for API key hashing; falls back to JWTSecret if empty
	NotificationKey    string // hex-encoded 32-byte AES-256 key for encrypting notification configs
	DiscordPublicKey      string // hex-encoded Ed25519 public key for Discord interaction verification
	SlackSigningSecret    string // Slack app signing secret for request verification
	TelegramWebhookSecret string // secret token for Telegram webhook verification
	TelegramBotToken      string // Telegram bot API token for editing messages after ack/resolve
	RegistrationEnabled   bool   // when true, allow new org+user registration via POST /auth/register
	TrustedProxyCIDRs     string // comma-separated CIDRs or "cloudflare"  - proxy headers trusted only from these IPs
	DevSeed            bool   // when true, populate database with mock development data
	ProConfig                 // Pro/Enterprise fields (empty struct in FOSS)
}

func Load() *Config {
	c := &Config{
		ListenAddr:      envOr("YIPYAP_LISTEN", ":8080"),
		DBDsn:           envOr("YIPYAP_DB_DSN", "yipyap.db"),
		JWTSecret:       envOr("YIPYAP_JWT_SECRET", ""),
		JWTExpiry:       envDuration("YIPYAP_JWT_EXPIRY", 24*time.Hour),
		APIKeySecret:    envOr("YIPYAP_API_KEY_SECRET", ""),
		NotificationKey:  envOr("YIPYAP_NOTIFICATION_KEY", ""),
		DiscordPublicKey:      envOr("YIPYAP_DISCORD_PUBLIC_KEY", ""),
		SlackSigningSecret:    envOr("YIPYAP_SLACK_SIGNING_SECRET", ""),
		TelegramWebhookSecret: envOr("YIPYAP_TELEGRAM_WEBHOOK_SECRET", ""),
		TelegramBotToken:      envOr("YIPYAP_TELEGRAM_BOT_TOKEN", ""),
		RegistrationEnabled: envBool("YIPYAP_REGISTRATION_ENABLED", true),
		TrustedProxyCIDRs:   envOr("YIPYAP_TRUSTED_PROXY_CIDRS", ""),
		DevSeed:             envBool("YIPYAP_DEV_SEED", false),
	}
	loadProConfig(c)
	return c
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return fallback
}

