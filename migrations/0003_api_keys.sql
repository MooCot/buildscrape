-- 0003_api_keys.sql — RBAC для admin API (POST /admin/sources,
-- PATCH .../enabled, POST .../archive). Заменяет единственный ADMIN_TOKEN
-- (единственная защита, без ролей и ротации — CLAUDE.md "Не сделано").
--
-- permission — закрытый набор, 1:1 с реальными admin-хендлерами (F7/F8/F9,
-- admin_handler.go); ENUM, не TEXT[] без ограничений — СУБД отклонит
-- неизвестное значение сама, не только комментарий в коде (тот же принцип
-- строгости, что CHECK/FK в 0001_init.sql). Новое право в будущем — через
-- ALTER TYPE permission ADD VALUE (PG12+, вне транзакции).
CREATE TYPE permission AS ENUM ('sources:write', 'sources:toggle', 'sources:archive');

-- roles — индирекция Subject -> Role -> Permissions: смысл в том, что
-- "operator" можно переопределить одной строкой для всех ключей сразу, не
-- трогая сами ключи.
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    permissions permission[] NOT NULL
);

-- api_keys — token_hash = SHA-256(raw secret), raw НИКОГДА не хранится и
-- не логируется. revoked_at NULL = активен (тот же NULL-паттерн, что
-- ai_breaker_state.opened_at, 0001_init.sql).
CREATE TABLE api_keys (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash  TEXT NOT NULL UNIQUE,
    label       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ
);

-- many-to-many в обе стороны: один ключ может нести несколько ролей, одна
-- роль назначена многим ключам — классический RBAC, не 1:1.
CREATE TABLE api_key_roles (
    api_key_id UUID NOT NULL REFERENCES api_keys(id),
    role_id    UUID NOT NULL REFERENCES roles(id),
    PRIMARY KEY (api_key_id, role_id)
);

-- Сидируются сразу, не только документируются — демо-данные должны быть
-- пригодны для первого `admin create-key --role=operator` без ручного
-- шага "сначала создай роль".
INSERT INTO roles (name, permissions) VALUES
    ('admin',     ARRAY['sources:write','sources:toggle','sources:archive']::permission[]),
    ('operator',  ARRAY['sources:toggle']::permission[]),
    ('publisher', ARRAY['sources:write']::permission[]);
