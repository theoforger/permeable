# permeable

A single-user Go service that pulls in multiple ICS calendar sources
(including Google Calendar's public ICS URLs), merges and filters them,
and republishes the result as one ICS subscription feed. It refreshes on
a schedule you set — not real-time — and is administered through a
server-rendered web UI, with no accounts and no login.

**Name origin:** events pass through a membrane of source defaults,
rules, exceptions, and filters — some make it through, some don't.

## Features

- Aggregate any number of ICS URL sources (Google Calendar, Outlook,
  etc.) plus "manual" sources you populate by uploading `.ics` files
  directly — useful for one-off events with no calendar of their own.
- RRULE recurrence expansion within a configurable date window.
- Filtering at three levels: a per-source default (include/exclude new
  events by default), global field-based filters (title/location/
  description), and per-event rules ("hide this one occurrence" /
  "hide all future occurrences of this title").
- A duration filter (min/max event length).
- Per-source health tracking and a `/stats` page.
- A background scheduler that republishes the merged feed on an interval
  you control from the UI, plus a "Refresh now" button.
- Full config export/import as JSON, for backup or migrating instances.
- Ships as a single static binary (no cgo) or a small Docker image.

## Quick start

### Bare binary

```bash
go build -o permeable ./cmd/permeable
./permeable
```

Then open `http://localhost:8080/` and add a source. The feed is served
at `http://localhost:8081/feed.ics` — see [Two ports](#two-ports-not-auth)
for why these are separate.

### Docker

```bash
docker build -t permeable .
docker run -d \
  --name permeable \
  -p 8081:8081 \
  -p 127.0.0.1:8080:8080 \
  -v permeable-data:/data \
  permeable
```

This publishes the feed port to the world and binds the admin port to
localhost only — adjust to match your actual network setup (see
[Deployment](#deployment) below). The SQLite database lives at
`/data/permeable.db` inside the container; the named volume is what
makes it survive `docker rm`/recreation.

Or, equivalently, with the included `docker-compose.yml`:

```bash
docker compose up -d --build
```

It builds from the local `Dockerfile`, binds the admin port to
`127.0.0.1` and publishes the feed port, and uses a named volume for
`/data` — the same defaults as the `docker run` example above. Both
published ports (and the admin port's bind address) are driven by
`.env` — `PERMEABLE_ADMIN_PORT`, `PERMEABLE_FEED_PORT`, and
`PERMEABLE_ADMIN_BIND_HOST` — so changing one variable moves both what
the app binds to inside the container and what's published on the host.
See [Configuration](#configuration) below.

## Configuration

Two kinds of settings, deliberately kept separate:

- **Process-level** (flags/env/`.env`, read once at startup): which
  ports to bind, where the SQLite file lives, and the *initial* poll
  interval (only used the very first time the database is created).
- **Everything else** (poll interval after that first run, feed TTL
  ceiling, date window, feed title, sources, filters, rules): stored in
  the database, edited from the settings UI (`/`), and applied live —
  no restart needed.

| Flag | Env var | Default | Meaning |
|---|---|---|---|
| `-admin-port` | `PERMEABLE_ADMIN_PORT` | `8080` | Admin UI port. Keep LAN-only. |
| `-feed-port` | `PERMEABLE_FEED_PORT` | `8081` | Feed-only port. Safe to expose. |
| `-db` | `PERMEABLE_DB_PATH` | `data/permeable.db` | SQLite file path. |
| `-refresh-minutes` | `PERMEABLE_REFRESH_MINUTES` | `60` | Seeds the poll interval on first run only. |

### `.env`

```bash
cp .env.example .env
# then edit .env
```

- **Bare binary:** `internal/config` loads `.env` from the working
  directory automatically (flags still take precedence over it).
- **Docker Compose:** `docker-compose.yml` reads `.env` via `env_file:`
  and injects it into the container's environment — the app itself never
  reads a `.env` file from inside the container (there isn't one; it's
  not copied into the image). The file is optional either way; without
  one, everything just uses its hardcoded default.
- **Plain `docker run`:** pass `--env-file .env` instead.

## Deployment

### Two ports, not auth {#two-ports-not-auth}

There's no login — this is a single-user tool. Instead, the admin UI
(settings, source/filter/rule CRUD, stats) and the public feed
(`GET /feed.ics`) are served on **two separate listeners**:

- Bind/firewall the **admin port** to your LAN or `localhost` only.
  Never put it behind a public reverse proxy — anyone who can reach it
  can reconfigure everything, with no auth prompt in the way.
- The **feed port** serves nothing but `GET /feed.ics`. This is the one
  port meant to be reverse-proxied or exposed to the internet, so a
  calendar app anywhere can subscribe.

This is enforced at the network layer (your firewall rules / which ports
you publish in Docker), not inside the app — there's no way to bypass it
by finding the "right" URL, because the admin routes simply don't exist
on the feed listener (and vice versa).

### `/healthz`

`GET /healthz` on the admin port does a plain up/DB-reachable check —
wire it into `docker-compose`'s `healthcheck:`, a Kubernetes probe, or
similar. The included `Dockerfile` already has a `HEALTHCHECK`
(distroless has no shell, so it execs a tiny purpose-built binary,
`cmd/healthcheck`, instead of `curl`).

### Reverse-proxying the feed port

Point your reverse proxy (Caddy, nginx, Traefik, ...) at the feed port
only, forwarding `GET /feed.ics`. No special headers or config needed —
it's a plain `text/calendar` response. TLS termination, rate limiting,
etc. are exactly what you'd do for any other static-ish endpoint.

## Usage

1. Add a source on `/` — `url` type for anything with an ICS link
   (Google Calendar: Settings → your calendar → "Secret address in iCal
   format"), or `manual` type to upload `.ics` files with no source URL
   of their own (single- or multi-event files both work; re-uploading a
   file with a UID you've already added updates that event in place).
2. Set each source's default: include new events by default, or exclude
   them by default (useful for a noisy calendar you only want specific
   things from).
3. Add global filters (title/location/description, include/exclude,
   case-insensitive substring match) and/or a duration filter for things
   that apply across every source.
4. Use `/events` to see every upcoming occurrence, what's currently
   included/excluded and why, and to hide a single occurrence or all
   future occurrences of a given title.
5. Subscribe your calendar app to the feed URL shown on `/` (the feed
   port). Most apps poll on their own schedule regardless of the
   published TTL, so also set the poll interval in `/config` to
   something reasonable for how often your sources actually change.
6. `/stats` shows the last generation's counts and per-source health if
   something isn't showing up as expected.

## Known limitations (v1)

- **`RECURRENCE-ID` overrides aren't applied.** A single moved/modified
  occurrence of a recurring event may still show at its original time.
  Revisit only if it proves necessary in practice — most calendar
  exports don't rely on this heavily.
- **All-day events** are included by default like any other event (no
  separate toggle), and duration filters see their real full-day (or
  multi-day) span — not special-cased. A max-duration filter is a valid
  way to hide multi-day retreats from a feed meant for meetings.
- **Export/import isn't transactional.** A failure partway through a
  large import can leave a partially-restored state. In practice this is
  rare — most failures are validation errors caught before anything is
  written.
- The `/events` page runs a **live fetch** of every enabled source on
  each load (independent of the scheduler's own cached feed) — that's
  intentional, so you can preview upcoming events before they're
  filtered/cached, but it means the page can be slow if you have many or
  slow sources.

## Development

```bash
go build ./...                          # compile everything
go run ./cmd/permeable                  # run locally (reads flags/.env)
go vet ./...                            # static checks
go test ./...                           # unit + integration tests
go test ./... -race                     # same, with the race detector
CGO_ENABLED=0 go build ./cmd/permeable  # sanity-check the no-cgo constraint
```

See `CLAUDE.md` for architecture, the full pipeline/precedence rules, and
database schema notes; `STAGES.md` for the build history.
