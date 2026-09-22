-- Adds an explicit include/exclude action to event_exceptions, mirroring
-- event_rules.action, so a single occurrence can be force-included (not
-- just hidden) — e.g. "only include this occurrence" from an otherwise
-- excluded/default_include=false source. Existing rows predate this
-- column and keep their original meaning via the default.
ALTER TABLE event_exceptions ADD COLUMN action TEXT NOT NULL DEFAULT 'exclude' CHECK (action IN ('include','exclude'));
