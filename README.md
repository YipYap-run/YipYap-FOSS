# YipYap Alerts - Open Source

Free, self-hosted monitoring and alerting for small teams. One binary, zero external dependencies.

![YipYap alerts dashboard](docs/screenshot-alerts.png)

## Features

- **HTTP, TCP, DNS, Ping, Heartbeat** health checks on configurable intervals
- **Escalation policies** with multi-step notification, repeats and loops
- **Personal contact methods and notification rules** so each person chooses how they get paged
- **Slack, Discord, Telegram, Email, Webhook, ntfy.sh, Pushover** notification channels
- **API key management** for automation and CI/CD integration
- **Multi-user** with role-based access (owner, admin, member, viewer)
- **Dashboard** with real-time monitor status, latency charts, uptime bars
- **Maintenance windows** with alert suppression
- **Custom monitor states** with per-monitor match rules
- **Monitor groups** for composite status rollup
- **Labels** for filtering and organizing monitors
- **Auto-resolve** to automatically close alerts when monitors recover
- **Monitor description** field for context and runbook notes
- **Mute/pause** to suppress notifications or stop checks entirely
- **Alert acknowledgment and resolution** workflow
- **SQLite** storage, single binary, zero external dependencies

## Quick Start

### Docker (recommended)

```bash
docker compose up -d
```

Open http://localhost:8080, register an account, and start adding monitors.

#### Non-root image and existing data volumes

The image runs as the unprivileged user `65534:65534` (nobody), not root. Its
working directory is `/data`, which is owned by that user, so a fresh named
volume mounted at `/data` is writable with no extra steps.

Data written by an older image that ran as root is owned by root, and the new
image cannot open it (the log shows a permission error on `yipyap.db` or
`jwt_secret`). Fix the ownership once before starting the new image:

```bash
docker compose stop yipyap

# Named volume (the default compose setup). The volume name is
# <project>_yipyap-data; `docker volume ls` shows it.
docker run --rm -v yipyap_yipyap-data:/data alpine:3 chown -R 65534:65534 /data

# Bind mount instead of a named volume:
sudo chown -R 65534:65534 ./data

docker compose up -d
```

On Kubernetes, set `securityContext.fsGroup: 65534` on the pod so the volume
is group-writable by the container user, or run the same `chown` once from an
init container.

Ping monitors use an unprivileged ICMP socket, so the container needs no extra
capabilities. Docker, Podman and containerd 2.x allow this by default. On an
older containerd, set the pod sysctl `net.ipv4.ping_group_range` to
`"0 2147483647"`.

### From Source

```bash
# Build
go build -o yipyap ./cmd/yipyap

# Run. With YIPYAP_JWT_SECRET unset, a secret is generated on first boot and
# stored in the per-user config dir (for example ~/.config/yipyap/jwt_secret).
# It is never written into the source checkout; set YIPYAP_DATA_DIR to choose
# another place.
./yipyap
```

### Development

```bash
# Seed with mock data
YIPYAP_DEV_SEED=true go run ./cmd/yipyap
```

Credentials are printed to the console on first run (randomly generated password).

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `YIPYAP_LISTEN` | `:8080` | HTTP listen address |
| `YIPYAP_DB_DSN` | `yipyap.db` | SQLite database file path |
| `YIPYAP_JWT_SECRET` | (generated) | JWT signing secret, at least 32 bytes. When unset, a random secret is generated on first boot and stored in `jwt_secret` in the data dir (mode 0600), then reused. Back that file up with the database: losing it signs everyone out. Known placeholder values and anything shorter than 32 bytes are refused at startup. |
| `YIPYAP_DATA_DIR` | directory of a SQLite DB path that names a directory, else `./jwt_secret` if it already exists, else the per-user config dir (`~/.config/yipyap` on Linux, `~/Library/Application Support/yipyap` on macOS) | Where the generated `jwt_secret` file lives. The Docker image sets `YIPYAP_DB_DSN=/data/yipyap.db` in compose, so it is the `/data` volume. A generated secret is never written into a directory that contains `go.mod` or `.git`; startup fails and asks for `YIPYAP_DATA_DIR`. |
| `YIPYAP_JWT_EXPIRY` | `24h` | JWT token expiry |
| `YIPYAP_SMTP_HOST` | (none) | SMTP server for account email: password resets, invites and contact method verification. Unset means no email |
| `YIPYAP_SMTP_PORT` | `587` | SMTP port (465 uses implicit TLS, others require STARTTLS) |
| `YIPYAP_SMTP_USER` / `YIPYAP_SMTP_PASS` | (none) | SMTP credentials |
| `YIPYAP_SMTP_FROM` | `noreply@localhost` | Sender address for account email |
| `YIPYAP_NOTIFICATION_KEY` | (none) | Hex-encoded 32-byte AES-256 key for encrypting notification channel configs |
| `YIPYAP_REGISTRATION_ENABLED` | `true` | Allow new org+user registration |
| `YIPYAP_DEV_SEED` | `false` | Populate database with mock development data |
| `YIPYAP_MAX_MONITORS_PER_ORG` | (no cap) | Optional cap on monitors per organization |
| `YIPYAP_MAX_NOTIFICATION_CHANNELS_PER_ORG` | (no cap) | Optional cap on notification channels per organization |
| `YIPYAP_MAX_ESCALATION_POLICIES` | (no cap) | Optional cap on escalation policies per organization |
| `YIPYAP_MAX_MEMBERS` | (no cap) | Optional cap on members per organization |
| `YIPYAP_MIN_CHECK_INTERVAL_SECONDS` | `10` | Shortest check interval a monitor may use |
| `YIPYAP_PUBLIC_BASE_URL` | (none) | Public URL of this server, used for acknowledge links in notifications, CORS and the WebSocket origin check |
| `YIPYAP_ALLOW_PRIVATE_TARGETS` | `true` | Allow monitors and notification channels to reach private and local addresses |
| `YIPYAP_TRUSTED_PROXY_CIDRS` | (none) | Proxies whose forwarded client IP headers are trusted (see `deploy/reverse-proxy/README.md`) |
| `YIPYAP_TELEGRAM_BOT_TOKEN` | (none) | Token of your Telegram bot, for Telegram channels and contact methods |
| `YIPYAP_TELEGRAM_BOT_USERNAME` | (none) | Username of that bot, shown in the channel form and used in the link command |
| `YIPYAP_TELEGRAM_WEBHOOK_SECRET` | (none) | Secret that Telegram sends with each webhook call to `/api/v1/webhooks/telegram` |

The open source build has no plan limits. The caps above are off unless you
set them. When a user hits one, the error names the variable so they know
who to ask.

## How people get paged

Escalation steps page people. Each user adds their own contact methods
under Settings, Contact Methods (email, webhook, ntfy, Pushover, PagerDuty,
Slack DM, Discord DM, Telegram), verifies them with a code (Telegram is
verified by Connect Telegram instead), and sets notification rules: which method, after how many minutes, for which alerts. Rules still waiting are dropped
once the alert is acknowledged. A step target can also post to a shared
notification channel as an extra destination. When the alert resolves, each
contact method and channel that was paged gets one resolve notification.

A personal webhook contact method with a signing secret signs every request
with that secret. Requests carry `X-YipYap-Timestamp` and
`X-YipYap-Signature: v1=<hex>`, where the signature is
HMAC-SHA256(secret, timestamp + "." + raw body).

Step options: wait time, repeat pages, page targets at once or one after
another, and stop escalating at a step.

## Notification Channels

All channels are configured through the web UI under Settings → Notification Channels.

| Channel | Configuration |
|---------|---------------|
| **Email (SMTP)** | Host, port, username, password, from/to addresses |
| **Webhook** | URL, payload format, custom headers. Requests are `POST` and signed with the channel's signing secret |
| **Slack** | Bot token + channel ID |
| **Discord** | Bot token + channel ID (for a Discord webhook URL, use a Webhook channel) |
| **Telegram** | Your bot's token, username and webhook secret (`YIPYAP_TELEGRAM_*` above); each chat is linked with a one-time command from Settings, Channels, such as `/link@your_bot CODE`. People add Telegram as a personal contact method with **Connect Telegram** under Settings, Contact Methods, which opens a private chat with the bot and adds the method already verified |
| **ntfy.sh** | Server URL + topic |
| **Pushover** | API token + user key |

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for setup instructions, code style, and guidelines.

## License

[AGPL-3.0](LICENSE)

## YipYap Cloud

The hosted service at [yipyap.run](https://yipyap.run) runs the same core and
adds features this edition does not include:

- Teams and on-call schedules with rotation (all plans)
- Service catalog with runbooks and dependencies (all plans)
- Two-factor sign-in with authenticator apps, security keys and passkeys (all plans)
- Slack and Discord apps with Acknowledge and Resolve buttons
- SSO / OIDC sign-in and org-wide MFA enforcement (Pro and Enterprise)
- SMS and voice call notifications (Pro and Enterprise)
- Public status pages and incidents (Pro and Enterprise)
- OpenTelemetry export of your metrics to your own collector (Pro and Enterprise)

See [plans and pricing](https://yipyap.run/pricing).
