package domain

import (
	"context"

	"github.com/google/uuid"
)

// Geocoder — outbound-порт (CLAUDE.md §3.4).
type Geocoder interface {
	Resolve(ctx context.Context, address string) (GeoResult, error)
}

// AddressCache — outbound-порт: кеш по нормализованному адресу (тот же
// текст не бьёт во внешний API повторно, CLAUDE.md §3.4).
type AddressCache interface {
	Get(ctx context.Context, normalizedAddress string) (GeoResult, bool, error)
	Set(ctx context.Context, normalizedAddress string, r GeoResult) error
}

type ItemGeoWriter interface {
	SaveGeo(ctx context.Context, itemID uuid.UUID, r GeoResult) error
}

// EventPublisher — pipeline.events (fanout), см.
// internal/shared/contracts.ItemProcessedEvent doc-comment.
type EventPublisher interface {
	PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error
}
