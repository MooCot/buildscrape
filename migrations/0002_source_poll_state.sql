-- 0002_source_poll_state.sql — курсор опроса источников.
--
-- Отдельная таблица, а не колонка sources.last_polled_at: sources целиком
-- принадлежит Storage (F7/F8/F9 — единственный писатель Source.*, MODEL-
-- buildscrape.md §1.6), а курсор опроса пишет ТОЛЬКО планировщик Fetch —
-- другой факт и другой писатель, смешивать их в одной строке значило бы
-- заново завести многописательную сущность там, где её нет.
--
-- next_poll_at (а не last_polled_at): условие "источник пора опрашивать"
-- сводится к next_poll_at <= now, и его можно перепроверить под блокировкой
-- строки в ON CONFLICT ... DO UPDATE ... WHERE — благодаря этому два
-- планировщика (Fetch-воркеры масштабируются --scale) не заберут один и тот
-- же источник дважды. См. fetch/adapter/outbound/source_scheduler.go.
CREATE TABLE source_poll_state (
    source_id    UUID PRIMARY KEY REFERENCES sources(id),
    next_poll_at TIMESTAMPTZ NOT NULL
);
