package domain

import (
	"context"

	"github.com/google/uuid"
)

// Extractor — outbound-порт: разобрать сырой HTML в поля карточки.
// Site-специфичен (вёрстка — свойство источника: два шаблона на одном сайте
// — норма, см. mocksource), поэтому за портом; домен оркестрирует, но не
// знает, где на странице лежит цена. Ошибка — страницу вообще нельзя
// разобрать (не HTML); "страница есть, но полей нет" — это не ошибка
// экстрактора, а невалидная карточка, её ловит валидация в домене.
type Extractor interface {
	Extract(rawHTML string) (Extracted, error)
}

// ItemWriter — идемпотентный upsert (UNIQUE(source_id, external_id) + ON
// CONFLICT, ТЕОРИЯ-УГЛУБЛЁННО.md §6.2) — повторная доставка того же
// сообщения не создаёт дубликат. Возвращает сохранённый Item (с
// проставленным ID — сгенерированным при первой вставке или уже
// существующим при повторном upsert того же (source_id, external_id)):
// НАЙДЕНО при написании теста — предыдущая версия принимала только error,
// и item.ID оставался вечным zero-value, потому что нигде не появлялся
// генерируемый БД идентификатор; все downstream-публикации ссылались на
// пустую строку вместо настоящего ID.
type ItemWriter interface {
	Upsert(ctx context.Context, item Item) (Item, error)
}

// NextStagePublisher — hand-off F2→{F3,F4,F5}, три независимых публикации
// (не одна общая) — ровно потому, что Media/Geocode/AI независимые
// параллельные писатели (MODEL-buildscrape.md §1.6, Item — контейнер ЖЦ).
// PublishMediaJob/PublishGeocodeJob вызываются условно (n/a-состояние —
// сообщение не публикуется вовсе, не публикуется-и-игнорируется);
// PublishAIJob — безусловно (AI n/a не бывает).
type NextStagePublisher interface {
	PublishMediaJob(ctx context.Context, itemID uuid.UUID, photoURLs []string) error // photoURLs пуст ⟹ Media ответит n/a
	PublishGeocodeJob(ctx context.Context, itemID uuid.UUID, address string) error
	PublishAIJob(ctx context.Context, itemID uuid.UUID, description string) error
}

// EventPublisher — pipeline.events (fanout), per-стадийный прогресс,
// см. contracts.ItemProcessedEvent doc-comment.
type EventPublisher interface {
	PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error
}
