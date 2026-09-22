# Permeable — Calendar Aggregator

Single-user Go service that pulls multiple ICS sources (including Google
Calendar public ICS URLs), merges and filters them, and republishes the
result as one ICS subscription feed. Periodic background refresh, not
real-time. Server-rendered UI, no auth, no accounts. Ships as a single
static binary; Docker is an alternative deploy path.

**Name origin:** events pass through a membrane of source defaults, rules,
exceptions, and filters — some make it through, some don't. Module/repo:
`permeable`. Binary: `permeable` (CLI alias `perm`).

Build plan and per-stage checklist: see `STAGES.md`. Do not jump ahead of
the current stage — each stage should build, and where noted, be verified
before moving to the next.

## Tech stack

| Concern | Choice | Why |
|---|---|---|
| Language | Go | statically typed, single-binary deploys |
| HTTP | stdlib `net/http` | few routes, no framework needed |
| Templates | `html/template` (stdlib) + `go:embed` | no build step, assets baked into binary |
| ICS parse/generate | `github.com/arran4/golang-ical` | best-maintained Go ICS library |
| RRULE expansion | `github.com/teambition/rrule-go` | recurring event expansion |
| DB | SQLite via `modernc.org/sqlite` (pure Go, no cgo) | keeps single-binary/cross-compile story intact |
| Scheduling | stdlib `time.Ticker` | fixed-interval refresh only; no cron-expression parsing |
| Config | flags + optional `.env` (`github.com/joho/godotenv`) | admin port, feed port, DB path, refresh interval |

**Hard constraint: no cgo.** `modernc.org/sqlite` is chosen specifically so
`CGO_ENABLED=0` works everywhere, including the distroless Docker build in
Stage 11. Never introduce a cgo-dependent package (e.g. `mattn/go-sqlite3`).

## Repository layout

Follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout)
conventions, kept minimal — only `cmd/` and `internal/` are adopted from it;
everything else in that reference (`pkg/`, `api/`, `scripts/`, etc.) is
skipped as unneeded for a single-binary, single-user app.

```
permeable/
├── cmd/
│   └── permeable/
│       └── main.go            # thin entrypoint: wire config → db → scheduler → server
├── internal/
│   ├── config/         # flags/.env loading
│   ├── db/              # sqlite connection + go:embed migrations (schema.sql lives here)
│   ├── model/            # Event, Source, FilterRule, etc.
│   ├── fetchsvc/         # concurrent fetch + parse + health tracking
│   ├── filtersvc/         # inclusion resolution: rules, exceptions, filters, duration
│   ├── feedsvc/            # ICS generation + feed_cache + stats
│   ├── exportsvc/           # JSON export/import of full config
│   ├── scheduler/            # cron wiring: fetch → filter → generate
│   └── web/
│       ├── handlers.go
│       └── templates/         # settings.html, events.html, stats.html (go:embed)
├── Dockerfile
├── go.mod
└── data/                        # sqlite file + generated feed (gitignored)
```

Keep each `internal/` package focused on the single responsibility named
above — e.g. don't let ICS generation logic leak into `fetchsvc`, and don't
let filter-resolution logic leak into `web`. `cmd/permeable/main.go` stays
thin: flag/env parsing lives in `internal/config`, everything else is wired
from there, not written inline in `main`.

## Database

Full schema lives in `schema.sql` — load it via `go:embed` migrations in
`internal/db`. Key tables and how they relate:

- **`sources`** — one row per calendar source. `type` is `url` or `manual`;
  `url` is required for `url`-type, null for `manual`-type. Carries per-source
  defaults (`default_include`, `title_prefix`) and last-fetch health fields.
- **`manual_events`** — VEVENTs attached directly to a `manual` source via
  file upload, no fetch step. Uniqueness on `(source_id, uid)` — re-uploading
  the same UID updates in place.
- **`config`** — single-row (`id = 1`) global settings: poll interval, feed
  TTL ceiling, date window, feed title.
- **`event_rules`** — per-source, per-title include/exclude for all future
  occurrences.
- **`event_exceptions`** — per-source, per-title, per-date include/exclude
  override (`action`) for a single occurrence. Takes precedence over
  `event_rules`.
- **`filters`** — global, cross-source, field-based (title/location/
  description/title-or-description) include/exclude.
- **`duration_filters`** — single-row (`id = 1`) global min/max event length.
- **`feed_cache`** — the generated output ICS plus last-run stats. `/feed.ics`
  always serves from here; nothing is computed on request.

## Pipeline (runs on each scheduler tick and on manual "refresh now")

1. Load enabled sources.
2. Fetch content per source, branching on `type`:
   - `url` — concurrent HTTP fetch (goroutines + `errgroup`); a failed fetch
     is logged and skipped, not fatal to the run.
   - `manual` — no network call; load rows from `manual_events`, parse each
     stored `raw_vevent`.
3. Write health fields back per source: `last_fetch_at`, `last_fetch_status`,
   `last_fetch_error`, `last_event_count`, `source_ttl_minutes` (parsed from
   `X-PUBLISHED-TTL` / `REFRESH-INTERVAL;VALUE=DURATION`, `url` sources only).
4. Parse each feed (`golang-ical`), normalize into `model.Event`.
   **All-day events** (`DTSTART;VALUE=DATE`) are decided explicitly (Stage 12):
   included by default like any other event — no separate toggle. They carry
   `AllDay=true` and are represented with DATE (not DATE-TIME) values start to
   end, all the way through to the generated feed. Duration filters are
   deliberately *not* special-cased for them: their length is their real
   full-day (or multi-day) span in minutes, so a max-duration filter is a
   legitimate way to hide multi-day retreats/vacations from a feed meant for
   meetings.
5. Expand recurrence (`rrule-go`) within `[now - days_back, now + days_forward]`.
   **Known v1 gap:** `RECURRENCE-ID` overrides (a single moved/modified
   occurrence) are not applied — a rescheduled instance may show at its
   original time. Revisit in Stage 12 only if it proves necessary.
6. Merge across sources, dedupe on UID where possible.
7. Resolve inclusion per event/occurrence, **in this order**:
   1. Source disabled → excluded
   2. `event_exceptions` match (source, title, this date) → use the
      exception's action, stop
   3. `event_rules` match (source, title) → use rule's action, stop
   4. Otherwise → use `source.default_include`
   5. Duration filter fails (`duration_filters`) → excluded
   6. Global `filters` (title/location/description/title-or-description,
      include/exclude) → apply
8. Apply `title_prefix` for display — after all matching, so prefixes never
   interfere with rule/filter matching.
9. Compute effective feed TTL: `min(most frequent declared source_ttl_minutes, config.max_feed_ttl_minutes)`.
   If no source declares a TTL, use `max_feed_ttl_minutes` alone.
10. Generate output `.ics` (`golang-ical`), writing both `X-PUBLISHED-TTL`
    and `REFRESH-INTERVAL;VALUE=DURATION` (ISO 8601 duration, e.g. `PT30M`)
    to the effective TTL.
11. Write `ics_content` + run stats to `feed_cache`.
12. `/feed.ics` always serves the cached content — no work on request.

Note: TTL fields are hints, not guarantees — most calendar apps poll
subscribed ICS feeds on their own schedule regardless. Still worth setting
correctly; it's the honest thing to publish and some clients do respect it.

## Endpoints

Served on **two separate `http.Server` listeners / ports**, so only the feed
port needs to go behind a reverse proxy / be exposed to the internet — the
admin UI stays LAN-only:

- **Admin port** (`-admin-port`, default `8080`) — settings UI, source/filter/
  rule CRUD, stats, healthz. Bind this to a LAN-only interface or firewall
  it off; never put it behind the public reverse proxy.
- **Feed port** (`-feed-port`, default `8081`) — serves only `GET /feed.ics`.
  This is the one port you point a reverse proxy / expose publicly, so
  external calendar clients can subscribe without reaching the admin UI.

Both listeners share the same DB handle and run in the same process; the
split is routing/binding only, not a second binary.

| Route | Port | Purpose |
|---|---|---|
| `GET /` | admin | Settings page: sources, filters, duration filters, poll interval, max feed TTL, feed URL, "Refresh now" |
| `POST /sources`, `POST /sources/{id}`, `POST /sources/{id}/delete` | admin | Source CRUD, incl. `type`, `title_prefix`, `default_include` |
| `POST /sources/{id}/events` | admin | Upload `.ics` to a `manual` source — every `VEVENT` found is inserted/updated by UID |
| `POST /sources/{id}/events/{event_id}/delete` | admin | Remove one manual event |
| `POST /filters`, `POST /filters/{id}/delete` | admin | Field-based include/exclude filters |
| `POST /duration-filters` | admin | Min/max event length |
| `POST /config` | admin | Poll interval, max feed TTL ceiling, date window, feed title |
| `GET /events` | admin | Per-occurrence list; "Hide"/"Show" this occurrence → `event_exceptions` (include or exclude), "Hide all"/"Show all" → `event_rules` |
| `GET /stats` | admin | Last generation time, pre/post-filter counts, per-source health table |
| `GET /healthz` | admin | Plain up/DB-reachable check, for Docker healthcheck (checked from inside the container/LAN, not the public port) |
| `GET /config/export` | admin | Full config as JSON |
| `POST /config/import` | admin | Load config JSON (replace or merge) |
| `GET /feed.ics` | **feed** | The output feed — the only route reachable on the feed port; subscribe to this URL elsewhere |

## Commands

```bash
go build ./...                     # compile everything
go run ./cmd/permeable             # run locally (reads flags/.env)
go vet ./...                        # run before considering any stage done
go test ./...                       # run before considering any stage done
CGO_ENABLED=0 go build ./cmd/permeable # sanity-check the no-cgo constraint before Stage 11
```

## Conventions

- No cgo, anywhere, ever — see "Hard constraint" above.
- Concurrent source fetches use `errgroup`; a single source failure must
  never abort the whole pipeline run.
- Templates and migrations are baked in via `go:embed` — no runtime file
  reads from disk for these.
- `config` and `duration_filters` are singleton tables (`CHECK (id = 1)`) —
  treat them as upsert-only, never insert a second row.
- Inclusion resolution must follow the exact precedence order in step 7
  above; exceptions beat rules beat defaults beat filters.
- Malformed `VEVENT`s are logged and skipped, never fatal to a source's
  fetch — implemented from Stage 3 onward, not deferred to Stage 12.
- Prefer small, single-purpose functions per package over large handler
  bodies — `web/handlers.go` should stay thin and delegate to the `*svc`
  packages.

## Working notes for Claude Code

- Work through `STAGES.md` sequentially; each stage lists its own scope and
  done-criteria. Don't implement later-stage features early even if
  convenient — e.g. don't wire the scheduler (Stage 9) while doing fetch
  (Stage 3).
- After finishing a stage: run `go build ./...`, `go vet ./...`, and
  `go test ./...`, then check the stage off in `STAGES.md`.
- Where a stage says "verify via logs/DB inspection," do so before moving on
  — don't defer verification to a later stage.
