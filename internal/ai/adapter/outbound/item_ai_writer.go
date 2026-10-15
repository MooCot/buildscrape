package outbound

import (
	"context"
	"encoding/json"
	"fmt"

	"buildscrape/internal/ai/domain"
	"buildscrape/internal/platform/db"
	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

type PostgresItemAIWriter struct{ pool db.Pool }

func NewPostgresItemAIWriter(p db.Pool) *PostgresItemAIWriter { return &PostgresItemAIWriter{pool: p} }

// SaveClassification — ai_tags маршалится в JSON руками (не полагаемся на
// автоматическое []string→jsonb кодирование pgx без проверки на реальной
// БД, см. общую оговорку — SQL этого инкремента не прогонялся против
// живого Postgres) и кастуется явно ($2::jsonb), а не доверяется implicit
// cast драйвера.
func (w *PostgresItemAIWriter) SaveClassification(ctx context.Context, itemID uuid.UUID, c domain.Classification) error {
	tagsJSON, err := json.Marshal(c.Tags)
	if err != nil {
		return fmt.Errorf("marshal ai_tags: %w", err)
	}
	n, err := w.pool.Exec(ctx, `
		UPDATE items SET ai_category = $1, ai_tags = $2::jsonb WHERE id = $3
	`, c.Category, tagsJSON, itemID)
	if err != nil {
		return fmt.Errorf("save classification: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("save classification: item %s: %w", itemID, shareddomain.ErrNotFound)
	}
	return nil
}

func (w *PostgresItemAIWriter) Block(ctx context.Context, itemID uuid.UUID, reason string) error {
	n, err := w.pool.Exec(ctx, `UPDATE items SET blocked_reason = $1 WHERE id = $2`, reason, itemID)
	if err != nil {
		return fmt.Errorf("block item: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("block item: item %s: %w", itemID, shareddomain.ErrNotFound)
	}
	return nil
}
