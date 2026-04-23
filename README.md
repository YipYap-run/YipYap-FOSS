# YipYap Alerts - Open Source

Free, self-hosted monitoring and alerting for small teams. One binary, zero external dependencies.

## Features

- **HTTP, TCP, DNS, Ping, Heartbeat** health checks on configurable intervals
- **Escalation policies** with multi-step notification
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
- **SQLite** by default, with optional PostgreSQL or MariaDB for larger deployments

## Quick Start

### Docker (recommended)

```bash
docker compose up -d
```

Open http://localhost:8080, register an account, and start adding monitors.

### From Source

```bash
# Build
go build -o yipyap ./cmd/yipyap

# Run
YIPYAP_JWT_SECRET=your-secret-here ./yipyap
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
| `YIPYAP_JWT_SECRET` | (generated) | JWT signing secret - **set this in production** |
| `YIPYAP_JWT_EXPIRY` | `24h` | JWT token expiry |
| `YIPYAP_NOTIFICATION_KEY` | (none) | Hex-encoded 32-byte AES-256 key for encrypting notification channel configs |
| `YIPYAP_REGISTRATION_ENABLED` | `true` | Allow new org+user registration |
| `YIPYAP_DEV_SEED` | `false` | Populate database with mock development data |

## Notification Channels

All channels are configured through the web UI under Settings → Notification Channels.

| Channel | Configuration |
|---------|---------------|
| **Email (SMTP)** | Host, port, username, password, from/to addresses |
| **Webhook** | URL, method, custom headers |
| **Slack** | Bot token + channel ID |
| **Discord** | Bot token + channel ID (webhook URL supported as fallback) |
| **Telegram** | Bot token + chat ID |
| **ntfy.sh** | Server URL + topic |
| **Pushover** | API token + user key |

## Contributing

Contributions welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for setup instructions, code style, and guidelines.

## License

[AGPL-3.0](LICENSE)

## YipYap Pro & Enterprise

Need more? [YipYap Pro](https://yipyap.run/pricing) adds:

- Teams and on-call scheduling with rotation
- Escalation loops and advanced retry policies
- SSO / OIDC authentication
- SMS and voice call notifications
- Public status pages
- OpenTelemetry metrics export

[Learn more at yipyap.run](https://yipyap.run)
