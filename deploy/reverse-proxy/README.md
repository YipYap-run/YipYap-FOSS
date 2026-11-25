# Reverse Proxy Examples

YipYap serves everything (API, WebSocket, and the embedded UI) on a single
HTTP port. In production you typically terminate TLS in front of it. This
directory contains example configurations for three popular reverse proxies.

| File | Proxy |
|---|---|
| `nginx.conf` | nginx, static config. Drop into `/etc/nginx/sites-available/`. |
| `Caddyfile` | Caddy, single file. Handles TLS automatically via Let's Encrypt. |
| `traefik-dynamic.yml` | Traefik file provider (pairs with the static config below). |
| `traefik-docker-labels.yml` | Traefik via docker-compose labels. |

## WebSocket upgrade

The YipYap UI uses a WebSocket connection at `/api/v1/ws` for live status
updates. Every example below passes the `Upgrade` / `Connection` headers
through, and Caddy / Traefik handle this automatically. If you adapt
another proxy, make sure WebSocket upgrades are forwarded or live status
will not update in the browser.

## Client IP forwarding

YipYap reads `X-Forwarded-For` and `X-Real-IP` for rate-limiting and audit
logging, but only from trusted proxies. Set `YIPYAP_TRUSTED_PROXY_CIDRS`
on the YipYap side (see [Configuration](https://github.com/YipYap-run/YipYap-FOSS/wiki/Configuration))
to the CIDR of your proxy network, or real client IPs will be reported as
the proxy address.

How YipYap picks the client IP when the peer is a trusted proxy:

- `CF-Connecting-IP` is used only when the peer is inside Cloudflare's
  published ranges (enable them with the `cloudflare` keyword). From any
  other proxy it is ignored, so a client cannot spoof it through Caddy,
  nginx or Traefik. The examples also strip it.
- `X-Forwarded-For` is read from the right. Entries inside
  `YIPYAP_TRUSTED_PROXY_CIDRS` are skipped, and the first address outside
  that list is the client. Anything to its left is client-controlled and
  ignored.
- `X-Real-IP` is used only when there is no `X-Forwarded-For` header.
- A value that is not a plain IP address is rejected, and the proxy's own
  address is used instead.

Keep `YIPYAP_TRUSTED_PROXY_CIDRS` as narrow as possible. Trusting a whole
private range (for example `172.16.0.0/12` on a docker host) lets any
container on that range forge client IPs and bypass per-IP rate limits.

## TLS certificates

The examples assume certificates already exist at the paths shown. For
nginx, use [certbot](https://certbot.eff.org/) or a similar tool. Caddy
and Traefik fetch certificates automatically from Let's Encrypt; adjust
the contact email before deploying.

## Reporting issues

If you spot a problem with one of these examples, open an issue on the
YipYap FOSS repository with the proxy name in the title.
