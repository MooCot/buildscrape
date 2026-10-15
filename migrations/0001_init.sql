-- 0001_init.sql — схема из CLAUDE.md §5, с полями, добавленными по итогам
-- моделирования (MODEL-buildscrape.md): items.blocked_reason (гейт A),
-- sources.archived (отдельно от enabled — см. CLAUDE.md §5, C6).

CREATE TABLE sources (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT NOT NULL,
    base_url          TEXT NOT NULL,
    poll_interval_sec INTEGER NOT NULL DEFAULT 300,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    archived          BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE raw_items (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id   UUID NOT NULL REFERENCES sources(id),
    external_id TEXT NOT NULL,
    raw_html    TEXT NOT NULL,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    status      TEXT NOT NULL DEFAULT 'fetched' -- fetched | parsed | failed
);
CREATE UNIQUE INDEX raw_items_source_external_uidx ON raw_items (source_id, external_id);
CREATE INDEX raw_items_status_idx ON raw_items (status) WHERE status = 'parsed'; -- под TTL-cleanup (F10)

CREATE TABLE items (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id      UUID NOT NULL REFERENCES sources(id),
    external_id    TEXT NOT NULL,
    title          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    price          NUMERIC(12, 2),
    address        TEXT,
    lat            DOUBLE PRECISION,
    lng            DOUBLE PRECISION,
    geocoded_at    TIMESTAMPTZ,
    ai_category    TEXT,
    ai_tags        JSONB,
    blocked_reason TEXT, -- гейт A (nullable) — MODEL-buildscrape.md §1.3/§6 C3/C8
    status         TEXT NOT NULL DEFAULT 'processing',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX items_source_external_uidx ON items (source_id, external_id); -- идемпотентный upsert
CREATE INDEX items_ai_tags_gin_idx ON items USING GIN (ai_tags);

CREATE TABLE item_media (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id      UUID NOT NULL REFERENCES items(id),
    s3_key       TEXT NOT NULL,
    kind         TEXT NOT NULL, -- thumbnail | preview | full
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE pipeline_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    item_id     UUID NOT NULL REFERENCES items(id),
    stage       TEXT NOT NULL,
    status      TEXT NOT NULL,
    error       TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pipeline_events_item_idx ON pipeline_events (item_id);

CREATE TABLE ai_breaker_state (
    provider     TEXT PRIMARY KEY,
    state        TEXT NOT NULL DEFAULT 'closed', -- closed | open | half_open
    opened_at    TIMESTAMPTZ
);
