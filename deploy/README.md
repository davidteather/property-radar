# Remote-hosting runbook

Everything needed to run `mcpd` on a VPS and talk to it from Claude Code over
Streamable HTTP with a bearer token.

## What runs where, and why

```text
LAPTOP (residential IP)                VPS (any provider)             CLIENT
  bin/ingest  ──crawls StreetEasy──▶     postgres:17                Claude Code
      │                                      ▲                          │
      │ DATABASE_URL over SSH tunnel ────────┘                          │
      └─ rsync thumbs ─────────▶  /srv/property-radar/deploy/thumbs     │
                                            │ (read-only)               │
                                        mcpd -transport http ◀──────────┘
                                            :8787  (bearer token)
```

**The crawler stays on the laptop.** StreetEasy blocks datacenter IPs, so a
residential egress path is part of the
[provider-access decision](../docs/decisions.md). Do not move `bin/ingest` onto
the VPS; it holds Postgres, the thumbnail cache, and the MCP server only.

## 1. Provision a VPS

Provider-agnostic; any 1 vCPU / 2 GB / 25 GB instance running a recent
Debian or Ubuntu is enough for the v0 corpus (a few hundred listings, ~1–3 GB
of thumbnails).

```bash
# on the VPS, as root
apt-get update && apt-get install -y ca-certificates curl rsync
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
  https://download.docker.com/linux/debian $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
  > /etc/apt/sources.list.d/docker.list
apt-get update && apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
```

Firewall: allow `22` (SSH) and whatever port terminates TLS (`443` with Caddy,
or `8787` if you expose `mcpd` directly). Do **not** open `5432`.

```bash
ufw default deny incoming && ufw allow 22 && ufw allow 443 && ufw enable
```

## 2. Check out the repo and configure

```bash
# on the VPS
git clone https://github.com/davidteather/property-radar /srv/property-radar
cd /srv/property-radar/deploy
cp env.example .env
openssl rand -hex 32   # -> POSTGRES_PASSWORD
openssl rand -hex 32   # -> MCP_BEARER_TOKEN
$EDITOR .env
mkdir -p /srv/property-radar/deploy/thumbs
```

`deploy/.env` is gitignored. `MCP_BEARER_TOKEN` is the only credential a client
needs; `mcpd` never logs it and never writes it to disk.

## 3. Bring the stack up

```bash
cd /srv/property-radar/deploy
docker compose up -d --build
docker compose ps
curl -fsS http://127.0.0.1:8787/healthz     # -> ok
curl -si  http://127.0.0.1:8787/ | head -1  # -> HTTP/1.1 401 Unauthorized
```

`db` is intentionally **not** published to the host or the internet. `mcpd`
reaches it on the compose network as `db:5432`.

## 4. Apply migrations

**`mcpd` applies the embedded migrations itself on startup** (`MIGRATE_ON_START`
defaults true; `-migrate=false` or `MIGRATE_ON_START=false` opts out). `mcpd`
owns migrations exclusively — it refuses to serve on a partial schema, so a bad
deploy fails loudly instead of quietly. The manual goose path below stays as a
fallback and for driving migrations out of band from the laptop.

Migrations are goose SQL files in `migrations/`. Nothing needs to be installed
on the VPS; run goose in a throwaway Go container attached to the compose
network:

```bash
cd /srv/property-radar
set -a && . deploy/.env && set +a
docker run --rm --network property-radar_default \
  -v /srv/property-radar/migrations:/migrations \
  golang:1.27-alpine \
  go run github.com/pressly/goose/v3/cmd/goose@v3.27.3 \
    -dir /migrations postgres \
    "postgres://property_radar:$POSTGRES_PASSWORD@db:5432/property_radar?sslmode=disable" up
```

Swap `up` for `status` to see what has been applied; re-running `up` is a no-op.
The same one-liner works **from the laptop** through the SSH tunnel from step 6
if you prefer to drive migrations from the repo you develop in:

```bash
go run github.com/pressly/goose/v3/cmd/goose@v3.27.3 \
  -dir ./migrations postgres \
  "postgres://property_radar:$POSTGRES_PASSWORD@127.0.0.1:15432/property_radar?sslmode=disable" up
```

## 5. TLS

`mcpd` speaks plain HTTP. Pick one of these; do not ship a bearer token over
cleartext HTTP across the public internet.

### Option A: Caddy (public HTTPS, automatic certificates)

Point a DNS A record at the VPS, set `MCP_PUBLISH_ADDR=127.0.0.1` in
`deploy/.env`, `docker compose up -d`, then:

```bash
apt-get install -y caddy
```

`/etc/caddy/Caddyfile`:

```caddyfile
radar.example.com {
	reverse_proxy 127.0.0.1:8787
}
```

```bash
systemctl reload caddy
```

That is the whole TLS story: Caddy provisions and renews a Let's Encrypt
certificate on first request. The client URL becomes `https://radar.example.com`
(port 443, no `:8787`).

### Option B: Tailscale (zero TLS config, no public exposure)

```bash
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up
ufw deny 8787   # nothing needs to be public
```

Set `MCP_PUBLISH_ADDR=0.0.0.0` and reach `mcpd` at
`http://<vps>.<tailnet>.ts.net:8787` from any device on the tailnet. The
network is already authenticated and encrypted; the bearer token stays as
defence in depth. Use `tailscale serve` if you want an HTTPS name inside the
tailnet.

## 6. Laptop side: nightly crawl + thumbnail sync

Build the binaries once (`make binaries` → `bin/ingest`).

Open an SSH tunnel to Postgres for the duration of the crawl; nothing else
needs the database port:

```bash
ssh -f -N -L 15432:127.0.0.1:5432 deploy@radar.example.com
```

That requires `db` to publish on the VPS loopback: uncomment the
`127.0.0.1:5432:5432` port mapping in `deploy/docker-compose.yml`. It is bound
to loopback, so only SSH sessions on the box can reach it.

Create `~/bin/property-radar-nightly.sh` on the laptop (edit the four paths at
the top; `POSTGRES_PASSWORD` comes from your shell profile or a keychain):

```bash
#!/usr/bin/env bash
set -euo pipefail

REPO=~/Documents/GitHub/property-radar
HOST=deploy@radar.example.com
LOCAL_THUMBS=~/.local/share/property-radar/thumbs
REMOTE_THUMBS=/srv/property-radar/deploy/thumbs

cd "$REPO"
ssh -f -N -L 15432:127.0.0.1:5432 "$HOST"
trap 'pkill -f "ssh -f -N -L 15432:127.0.0.1:5432 $HOST" || true' EXIT

export DATABASE_URL="postgres://property_radar:$POSTGRES_PASSWORD@127.0.0.1:15432/property_radar?sslmode=disable"
export THUMBS_DIR="$LOCAL_THUMBS"
# STORAGE_BACKEND defaults to local (this VPS recipe uses the rsynced disk cache).
# laptop-side only; get a key: https://go.dteather.com/webshare?src=property-radar&placement=vps-guide
export WEBSHARE_API_KEY=...

# -proxy-mode split = site pages via the pool, API direct (correct from a
# residential laptop IP). `-proxy` is a deprecated alias for the same thing.
bin/ingest -from-targets -proxy-mode split

rsync -az --chmod=D755,F644 "$LOCAL_THUMBS/" "$HOST:$REMOTE_THUMBS/"
```

Crontab (`crontab -e`), 03:15 nightly:

```cron
15 3 * * * /bin/bash -lc '~/bin/property-radar-nightly.sh >> ~/.local/state/property-radar-ingest.log 2>&1'
```

### Seed the crawl targets first

`-from-targets` crawls what the `crawl_targets` table says to crawl: every
enabled `standing` row plus every pending `once` row (those come from the
caller model's `request_crawl` tool). An empty table means the crawler does
nothing, so seed your standing scopes once, over the tunnel:

```bash
psql "postgres://property_radar:$POSTGRES_PASSWORD@127.0.0.1:15432/property_radar?sslmode=disable" -c "
INSERT INTO crawl_targets (kind, areas, listing_type, note)
VALUES ('standing', ARRAY['135','304','305','306','319','320','321','322','324','326','364'], 'sale',
        'Brownstone Brooklyn + UWS, v0 sale scope');"
```

Add a second row with `'rent'` for the rentals corpus. Keep the area set
identical to the `-areas` list you were passing before: the scope identity is a
hash of provider + areas + filters, so an unchanged set keeps the existing
volume baseline instead of starting a new one.

Operating the queue is plain SQL — `UPDATE crawl_targets SET enabled = false
WHERE id = 3` parks a standing scope, and
`SELECT id, kind, status, areas, last_error FROM crawl_targets ORDER BY id`
shows what the crawler will pick up. The model sees the same rows through
`list_crawl_targets`.

The old single-scope form still works for a one-off crawl:
`bin/ingest -areas 304,305 -listing-type sale`. Exactly one of `-areas` and
`-from-targets` must be given.

Notes:

- `-from-targets` exits non-zero if any target failed, was incomplete, or came
  back suspect, but one bad target never stops the others; each one logs its
  own `crawl target finished` line with `run_id` and `ok`.

- The crawl writes canonical rows straight into the remote Postgres over the
  tunnel; there is no separate publish step.
- Thumbnails are content-addressed and evicted only on replacement, delist, or age (a
  [photo-storage decision](../docs/decisions.md)), so `rsync` moves only
  genuinely new files each night.
- `--chmod=D755,F644` matters: `mcpd` runs as the distroless `nonroot` user and
  needs world-readable thumbnails.
- Keep the crawler on the laptop's residential connection; running it from the
  VPS gets the provider requests blocked.

## 7. Client side: register the server with Claude Code

Verified against `claude mcp add --help`:

```bash
claude mcp add property-radar --transport http https://radar.example.com \
  --header "Authorization: Bearer $MCP_BEARER_TOKEN"
```

Direct-exposure or Tailscale variant (explicit port, no TLS proxy):

```bash
claude mcp add property-radar --transport http http://radar.example.com:8787 \
  --header "Authorization: Bearer $MCP_BEARER_TOKEN"
```

Add `--scope user` to make it available in every project rather than just the
current directory. Verify with `claude mcp list`, then ask the model to call
`get_state`.

**claude.ai / ChatGPT web connectors** need the token in the URL: set
`URL_TOKEN_AUTH=true` (or pass `-url-token`) and connect to
`https://host/?token=<MCP_BEARER_TOKEN>`; the token then appears in that
client's URLs and in request logs, which is the trade-off. Full app-directory
OAuth 2.1 (dynamic client registration, `/.well-known/oauth-*`) is out of
scope; `mcpd` implements only the bearer scheme for the single-operator case.

## 8. Operating it

```bash
docker compose logs -f mcpd        # one info line per request; no headers logged
docker compose pull && docker compose up -d --build   # upgrade
docker compose exec db pg_dump -U property_radar property_radar | gzip > backup.sql.gz
```

Uptime monitoring: `GET /healthz` returns `200 ok` without authentication. The
other open paths are the docs shell (`/docs`, `/openapi.json`, `/openapi.yaml`,
`/schemas/*`, favicons), the `/connect` help page, and the `/img/*` photo proxy
(gated by `PUBLIC_IMG_TOKEN` when set). The MCP root and every `/v1` route
require `Authorization: Bearer <MCP_BEARER_TOKEN>` and answer `401` otherwise.

Token rotation: change `MCP_BEARER_TOKEN` in `deploy/.env`, `docker compose up
-d mcpd`, then re-run `claude mcp add` (after `claude mcp remove
property-radar`) on each client.

## Decisions you must make at deploy time

| Decision | Options |
|---|---|
| TLS | Caddy for a public HTTPS hostname, or Tailscale for a private tailnet with no public port. Plain HTTP on the open internet is not acceptable; the token would travel in cleartext. |
| Postgres reachability | Keep `db` unpublished and tunnel over SSH for crawls and migrations (default, recommended), or deliberately publish `127.0.0.1:5432` (still loopback-only) if you tunnel constantly. Never publish `0.0.0.0:5432`. |
| Thumbnail location | `THUMBS_HOST_DIR` bind mount (default `./thumbs`) so `rsync` can write into it as an ordinary user. A named Docker volume also works but requires root to rsync into `/var/lib/docker/volumes/...`. |
