// Package contracts — формат сообщений между стадиями через RabbitMQ.
// Единственная канонический источник истины для того, что летит по
// каждой очереди — продюсер (adapter/outbound одной стадии) и консюмер
// (adapter/inbound другой) ссылаются на один и тот же тип, не
// договариваются о JSON-форме порознь (тот же класс дублирования, что уже
// дважды находился в этой сессии — здесь предотвращается заранее, не
// постфактум).
//
// Импортируется ТОЛЬКО adapter-пакетами (inbound/outbound), НИКОГДА
// domain — domain работает со своими типами (Item, RawItem, ...), перевод
// wire-формат ↔ доменная модель — работа адаптера (anti-corruption слой),
// тот же принцип, что уже разделяет domain/adapter внутри одного
// хексагона, просто на границе очереди, а не только БД/HTTP.
package contracts

import "github.com/google/uuid"

// Envelope — общие поля каждого сообщения. ItemID — сквозной ключ через
// весь pipeline (CLAUDE.md §4.4), сгенерирован на Fetch-стадии, дальше не
// переприсваивается; uuid.UUID (не string) — сериализация в JSON и обратно
// через MarshalJSON/UnmarshalJSON из google/uuid, без ручного
// Parse/String() на каждой границе. PipelineVersion — pipeline_version на
// момент публикации (internal/core.CurrentPipelineVersion) — событие,
// записанное под старой версией, должно оставаться интерпретируемым и
// после того, как схема/пороги изменятся
// (internal/core/pipeline_specs.go doc-comment).
type Envelope struct {
	ItemID          uuid.UUID `json:"item_id"`
	PipelineVersion string    `json:"pipeline_version"`
}

// FetchJob — планировщик Fetch → fetch.queue → воркеры Fetch (F1a → F1b):
// "скачай эту страницу". Envelope НЕ встраивается сознательно: item_id
// рождается только при успешной загрузке (CLAUDE.md §4.4 — "сгенерирован
// на Fetch-стадии"), на момент задания item'а ещё нет, а нулевой uuid в
// обязательном поле контракта был бы ложью.
type FetchJob struct {
	PipelineVersion string    `json:"pipeline_version"`
	SourceID        uuid.UUID `json:"source_id"`
	URL             string    `json:"url"`
	ExternalID      string    `json:"external_id"`
}

// ParseJob — fetch.exchange → parse.queue (F1 → F2). Envelope.ItemID здесь
// — это ID самого RawItem, сгенерированный на Fetch и, per CLAUDE.md §4.4,
// переносимый как БУДУЩИЙ Item.ID неизменным (не перегенерируется на
// Parse) — кандидат для INSERT, который upsert обязан сохранить, а не
// заменить сгенерированным DB, кроме случая конфликта по (source_id,
// external_id), где сохраняется УЖЕ существующий id (см.
// parse/adapter/outbound/item_repo.go TODO). ExternalID — НАЙДЕНО при
// сверке сигнатур: раньше в контракте не было вообще, и inbound-консюмер
// ошибочно передавал сюда Envelope.ItemID вместо настоящего внешнего id.
type ParseJob struct {
	Envelope
	SourceID   uuid.UUID `json:"source_id"`
	ExternalID string    `json:"external_id"`
	RawHTML    string    `json:"raw_html"`
}

// MediaJob — parse.exchange → media.queue (F2 → F3). Публикуется, только
// если у источника есть photo-ссылки — иначе Media-состояние сразу n/a
// (MODEL-buildscrape.md §1.6), сообщение не создаётся вовсе, не
// публикуется-и-игнорируется.
type MediaJob struct {
	Envelope
	PhotoURLs []string `json:"photo_urls"`
}

// GeocodeJob — parse.exchange → geocode.queue (F2 → F4). Публикуется,
// только если Item.Address не пуст (иначе Geocode-состояние n/a).
type GeocodeJob struct {
	Envelope
	Address string `json:"address"`
}

// AIJob — parse.exchange → ai.queue (F2 → F5). AI-состояние n/a не бывает
// (MODEL §1.6) — публикуется всегда, если Item не заблокирован на этапе
// Parse.
type AIJob struct {
	Envelope
	Description string `json:"description"`
}

// ItemProcessedEvent — pipeline.events (fanout). Per-стадийное событие
// прогресса ("эта стадия для этого item завершена"), не единое "item
// полностью готов" — единого владельца композитной фазы Item нет
// (MODEL-buildscrape.md §1.6: F6 вычисляет Item-фазу на лету, не пишет
// отдельным полем); подписчики (dashboard) агрегируют по ItemID сами.
type ItemProcessedEvent struct {
	Envelope
	Stage  string `json:"stage"`  // fetch | parse | media | geocode | ai
	Status string `json:"status"` // done | error
	Error  string `json:"error,omitempty"`
}
