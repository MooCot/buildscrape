package domain

import (
	"context"

	"github.com/google/uuid"
)

// ImageProcessor — outbound-порт: cropping до стандартных размеров,
// watermark (CLAUDE.md §3.3).
type ImageProcessor interface {
	Process(ctx context.Context, sourceURL string, kind MediaKind) ([]byte, error)
}

// ObjectStore — outbound-порт: S3-совместимое хранилище (MinIO локально).
type ObjectStore interface {
	Put(ctx context.Context, key string, data []byte) error
}

type MediaWriter interface {
	Save(ctx context.Context, m ItemMedia) error
}

// EventPublisher — pipeline.events (fanout), см.
// internal/shared/contracts.ItemProcessedEvent doc-comment.
type EventPublisher interface {
	PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error
}
