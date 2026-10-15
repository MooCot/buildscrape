package domain

import (
	"context"

	"github.com/google/uuid"
)

// ItemReader — только чтение (F6 read-only, CLAUDE.md §5 F6 таблица).
type ItemReader interface {
	List(ctx context.Context, filter ItemFilter) ([]ItemView, error)
	Get(ctx context.Context, id uuid.UUID) (ItemView, error)
}

type ItemFilter struct {
	SourceID uuid.UUID
	Query    string
}
