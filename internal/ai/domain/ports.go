package domain

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Classifier — outbound-порт: чем именно классифицируем (LLM). Несколько
// провайдеров с fallback (CLAUDE.md §3.5).
type Classifier interface {
	Classify(ctx context.Context, description string) (Classification, error)
}

// BreakerSnapshot — персистентное состояние per-provider breaker. Одной
// State-константы недостаточно для корректной гидратации: без OpenedAt
// восстановленный Open-breaker считал бы cooldown уже истёкшим (zero-value
// time.Time выглядит как "открыт очень давно" в сравнении time.Since(...) >=
// CircuitBreakerCooldown) — НАЙДЕНО при написании
// TestClassifyItem_BreakerStateSurvivesRestart, не спроектировано заранее.
type BreakerSnapshot struct {
	State    State
	OpenedAt time.Time
}

// BreakerStore — outbound-порт: где хранится состояние circuit breaker
// per-provider (переживает рестарт процесса — Postgres, не in-memory).
type BreakerStore interface {
	Load(ctx context.Context, provider string) (BreakerSnapshot, error)
	Save(ctx context.Context, provider string, snap BreakerSnapshot) error
}

type ItemAIWriter interface {
	SaveClassification(ctx context.Context, itemID uuid.UUID, c Classification) error
	Block(ctx context.Context, itemID uuid.UUID, reason string) error // Шов — F5-сторона гейта A
}

// EventPublisher — pipeline.events (fanout), см.
// internal/shared/contracts.ItemProcessedEvent doc-comment.
type EventPublisher interface {
	PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error
}
