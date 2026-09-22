-- Permeable — initial schema
-- Embedded via go:embed in internal/db and applied as migration 0001.

CREATE TABLE sources (
    id                  INTEGER PRIMARY KEY,
    name                TEXT NOT NULL,
    type                TEXT NOT NULL DEFAULT 'url' CHECK (type IN ('url','manual')),
    url                 TEXT,                        -- required when type='url'; null for 'manual'
    enabled             BOOLEAN NOT NULL DEFAULT 1,
    default_include     BOOLEAN NOT NULL DEFAULT 1, -- new events included unless opted in/out
    title_prefix        TEXT,                        -- e.g. "[Work]"; null = no prefix
    last_fetch_at       DATETIME,
    last_fetch_status   TEXT,                         -- 'ok' | 'error'
    last_fetch_error    TEXT,
    last_event_count    INTEGER,
    source_ttl_minutes  INTEGER,                    -- parsed from X-PUBLISHED-TTL / REFRESH-INTERVAL, if present; url sources only
    created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (type != 'url' OR url IS NOT NULL)
);

-- events attached directly to a 'manual' source, added via file upload (no fetch step)
CREATE TABLE manual_events (
    id          INTEGER PRIMARY KEY,
    source_id   INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    uid         TEXT NOT NULL,          -- VEVENT UID; re-uploading the same UID updates in place
    raw_vevent  TEXT NOT NULL,          -- the VEVENT block as uploaded
    added_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (source_id, uid)
);

CREATE TABLE config (
    id                        INTEGER PRIMARY KEY CHECK (id = 1),
    refresh_minutes           INTEGER NOT NULL DEFAULT 60,  -- how often *this app* polls sources
    max_feed_ttl_minutes      INTEGER NOT NULL DEFAULT 60,   -- ceiling the user sets for output feed TTL
    days_back                 INTEGER NOT NULL DEFAULT 7,
    days_forward              INTEGER NOT NULL DEFAULT 90,
    feed_title                TEXT NOT NULL DEFAULT 'My Aggregated Calendar'
);

-- "hide all future events with this name" / "always include" (per source)
CREATE TABLE event_rules (
    id                INTEGER PRIMARY KEY,
    source_id         INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    title_normalized  TEXT NOT NULL,
    action            TEXT NOT NULL CHECK (action IN ('include','exclude')),
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (source_id, title_normalized)
);

-- "hide just this one occurrence" (takes precedence over event_rules)
CREATE TABLE event_exceptions (
    id                INTEGER PRIMARY KEY,
    source_id         INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    title_normalized  TEXT NOT NULL,
    occurrence_date   DATE NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (source_id, title_normalized, occurrence_date)
);

-- global cross-source filters, field-based
CREATE TABLE filters (
    id      INTEGER PRIMARY KEY,
    field   TEXT NOT NULL DEFAULT 'title',  -- 'title' | 'location' | 'description'
    type    TEXT NOT NULL CHECK (type IN ('include','exclude')),
    value   TEXT NOT NULL
);

-- single global min/max duration setting, same one-row pattern as `config`
CREATE TABLE duration_filters (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    min_minutes   INTEGER,   -- null = no minimum
    max_minutes   INTEGER    -- null = no maximum
);

-- cached generated feed + last-run stats
CREATE TABLE feed_cache (
    id                          INTEGER PRIMARY KEY CHECK (id = 1),
    ics_content                 TEXT NOT NULL,
    generated_at                DATETIME NOT NULL,
    total_events_pre_filter     INTEGER,
    total_events_post_filter    INTEGER,
    sources_ok                  INTEGER,
    sources_error               INTEGER,
    effective_ttl_minutes       INTEGER   -- the TTL actually written into the output feed
);
