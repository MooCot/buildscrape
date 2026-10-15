package domain

import (
	"context"
	"time"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// ProxyProvider — outbound-порт: ротация "прокси" (CLAUDE.md §3.1) — мок:
// пул фейковых эндпоинтов, health-check, исключение "мёртвого" на срок d.
type ProxyProvider interface {
	Acquire(ctx context.Context) (Proxy, error)
	MarkDead(ctx context.Context, p Proxy, d time.Duration)
}

type Proxy struct {
	Endpoint string
}

// PageFetcher — outbound-порт: один HTTP GET через данный прокси. error —
// только транспортный сбой (нет соединения/таймаут); любой полученный ответ,
// включая 4xx/5xx, — это Page, классификацию по статусу делает домен.
type PageFetcher interface {
	Get(ctx context.Context, proxy Proxy, url string) (Page, error)
}

// IndexExtractor — outbound-порт: вытащить объявления и ссылку "дальше" из
// страницы индекса. Site-специфичен (вёрстка и схема URL — свойства
// источника), поэтому за портом, а не в домене; pageURL нужен, чтобы
// разрешить относительные ссылки.
type IndexExtractor interface {
	ExtractIndex(html, pageURL string) (IndexPage, error)
}

// SourceScheduler — outbound-порт: АТОМАРНО забрать источники, которые пора
// опрашивать, и сдвинуть им следующий срок. "Забрать" = одновременно и
// выбрать, и пометить: два планировщика (Fetch-воркеры масштабируются) не
// должны получить один и тот же источник. Фильтра по фазе здесь НЕТ
// намеренно — фаза источника (C6) решается ровно одним местом, Phase(),
// которое F1 консультирует напрямую (P1); дублировать формулу в SQL
// планировщика значило бы завести ещё одну её копию.
type SourceScheduler interface {
	ClaimDue(ctx context.Context, now time.Time) ([]shareddomain.Source, error)
}

// RawItemWriter — outbound-порт записи результата (Шов F1/F2, см.
// MODEL-buildscrape.md §1.2).
type RawItemWriter interface {
	Save(ctx context.Context, item RawItem) error
}

// JobPublisher — outbound-порт: публикация задания САМОЙ себе (fetch.queue),
// F1a → F1b. Domain передаёт свои поля, не JSON (контракты — забота адаптера).
type JobPublisher interface {
	PublishFetchJob(ctx context.Context, sourceID uuid.UUID, url, externalID string) error
}

// NextStagePublisher — outbound-порт: hand-off F1→F2 (ParseJob).
type NextStagePublisher interface {
	PublishParseJob(ctx context.Context, rawItemID, sourceID uuid.UUID, externalID, rawHTML string) error
}
