# Permeable — Build Stages

Work through these in order. Check a box only after the stage's own
done-criteria are met and `go build ./...`, `go vet ./...`, `go test ./...`
all pass. See `CLAUDE.md` for architecture, schema, and pipeline detail —
this file is the sequencing/checklist layer only.

- [x] **Stage 1 — Skeleton**
  - `go.mod`, project layout per `CLAUDE.md`, config loading (flags/.env).
  - SQLite connection + migrations applying `schema.sql` via `go:embed`.
  - Done when: builds and runs against an empty DB with no errors.

- [x] **Stage 2 — Source management**
  - CRUD for `sources`, including `type` (`url`/`manual`), `title_prefix`,
    `default_include` from the start.
  - `url` sources: name + URL fields.
  - `manual` sources: created with just a name; events added afterward via
    `/sources/{id}/events`.
  - Manual source detail view: upload control accepting single- or
    multi-event `.ics` (every `VEVENT` found gets inserted); list of
    currently-added events with per-event remove; re-uploading a matching
    UID updates in place, not a duplicate.
  - Basic list UI on `/`, showing type per source.
  - Done when: sources of both types can be created, edited, deleted, and
    manual events can be uploaded/removed through the UI.

- [x] **Stage 3 — Fetch + normalize**
  - Concurrent fetch of `url` sources (goroutines + `errgroup`).
  - `manual` sources read directly from `manual_events` — no network call.
  - Both branches converge on one parse step into `model.Event`.
  - Health fields (`last_fetch_at`, status, error, count) written per
    source on every run, regardless of type.
  - Done when: verified via logs/DB inspection — no UI dependency yet.

- [x] **Stage 4 — Recurrence + merge**
  - RRULE expansion within the configured date window.
  - Cross-source merge + UID-based dedupe.
  - Done when: expanded/merged event set is correct for a test fixture with
    at least one recurring event and one duplicate UID across sources.

- [x] **Stage 5 — Filtering**
  - Field-based global filters (title/location/description, include/exclude).
  - Duration filters (min/max minutes).
  - Inclusion resolution wired end-to-end except rules/exceptions (Stage 6).
  - Done when: filters and duration bounds visibly change the resolved
    event set.

- [x] **Stage 6 — Event-level rules**
  - `/events` page: per-occurrence list with "hide this occurrence"
    (→ `event_exceptions`) and "hide all future like this" (→ `event_rules`).
  - View/remove existing rules per source.
  - `default_include` toggle surfaced in source settings.
  - Done when: precedence order from `CLAUDE.md` step 7 is exercised and
    correct — exceptions beat rules beat defaults beat filters.

- [x] **Stage 7 — Feed generation + serving**
  - ICS output generation; `feed_cache` writes including stats fields.
  - Parse `X-PUBLISHED-TTL` / `REFRESH-INTERVAL` during fetch (extends
    Stage 3), store as `source_ttl_minutes`.
  - Compute effective TTL and write both `X-PUBLISHED-TTL` and
    `REFRESH-INTERVAL;VALUE=DURATION` into the generated feed.
  - `max_feed_ttl_minutes` added to the config form.
  - `/feed.ics` serves from cache.
  - Done when: a subscribed test client can load `/feed.ics` and see
    correctly filtered events with a sane TTL.

- [x] **Stage 8 — Health & stats UI**
  - `/stats` page/panel: last run time, event counts, per-source status
    table (incl. declared TTL if any) and the effective TTL being published.
  - `/healthz` endpoint.
  - Done when: both routes render/respond correctly after a real run.

- [x] **Stage 9 — Scheduler**
  - `time.Ticker` on `refresh_minutes`, reset when the config value changes.
  - "Refresh now" button triggers the same pipeline function on demand.
  - Done when: changing the interval in config visibly changes tick timing
    without a restart.

- [x] **Stage 10 — Export/import**
  - `/config/export` — full JSON dump (sources, `manual_events`, filters,
    duration_filters, event_rules, event_exceptions, config).
  - `/config/import` with replace-or-merge choice in the UI.
  - Done when: exporting then importing into a fresh DB reproduces the
    original state.

- [x] **Stage 11 — Dockerize**
  - Multi-stage `Dockerfile`: build with `CGO_ENABLED=0`, final stage
    `gcr.io/distroless/static` (needed for CA trust store on HTTPS fetches),
    not bare `scratch`.
  - SQLite file on a mounted volume (`/data/permeable.db`).
  - Done when: binary-on-VPS and containerized runs behave identically
    against the same test sources.

- [x] **Stage 12 — Polish**
  - Defensive parsing: malformed `VEVENT`s logged and skipped, not fatal.
  - All-day event handling decided explicitly (included by default, or a
    dedicated toggle).
  - Revisit `RECURRENCE-ID` override handling (Stage 4 gap) only if it
    proves necessary in practice.
  - Logging pass, README, basic input validation on forms.
  - Done when: the above are addressed and the app is ready to run
    unattended.
