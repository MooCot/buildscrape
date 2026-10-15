package outbound

import (
	"context"
	"fmt"

	"buildscrape/internal/parse/domain"
	"buildscrape/internal/platform/db"
)

// PostgresItemWriter — реализует parse/domain.ItemWriter. id — НЕ в SET
// намеренно: при первой вставке сохраняется $1 (candidateID, item_id от
// Fetch, CLAUDE.md §4.4); при конфликте (повторный parse того же
// source_id+external_id) RETURNING возвращает УЖЕ существующий id — тот же
// логический Item не должен менять идентичность между повторными
// прогонами pipeline (см. parse/domain/service.go doc-comment на Parse).
//
// Повторный фетч, попавший на CAPTCHA-заглушку (Extracted.BlockedReason !=
// nil, полей карточки нет), НЕ затирает контент ранее хорошего item пустыми
// значениями: CASE оставляет items.title/description/price/address как есть
// и обновляет только blocked_reason. Следующий хороший фетч снимет блок
// (blocked_reason = NULL) и освежит контент. Заметьте: это же значит, что
// перепарсинг снимет и блок, поставленный AI-стадией (Шов, MODEL §1.3) —
// принято: Parse после этого заново публикует AIJob, и AI выставит его снова,
// если контент по-прежнему нарушает политику.
//
// SET-список — только поля, которые ВЛАДЕЕТ Parse (title/description/
// price/address/blocked_reason). lat/lng/geocoded_at/ai_category/ai_tags —
// НЕ трогаются здесь ни при вставке (остаются NULL), ни при обновлении
// (повторный parse не должен стирать уже написанные Geocode/AI результаты
// — конфликт с независимыми параллельными писателями, MODEL §1.6).
type PostgresItemWriter struct {
	pool db.Pool
}

func NewPostgresItemWriter(p db.Pool) *PostgresItemWriter {
	return &PostgresItemWriter{pool: p}
}

func (w *PostgresItemWriter) Upsert(ctx context.Context, item domain.Item) (domain.Item, error) {
	rows, err := w.pool.Query(ctx, `
		INSERT INTO items (id, source_id, external_id, title, description, price, address, blocked_reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source_id, external_id) DO UPDATE SET
			title          = CASE WHEN EXCLUDED.blocked_reason IS NULL THEN EXCLUDED.title       ELSE items.title       END,
			description    = CASE WHEN EXCLUDED.blocked_reason IS NULL THEN EXCLUDED.description ELSE items.description END,
			price          = CASE WHEN EXCLUDED.blocked_reason IS NULL THEN EXCLUDED.price       ELSE items.price       END,
			address        = CASE WHEN EXCLUDED.blocked_reason IS NULL THEN EXCLUDED.address     ELSE items.address     END,
			blocked_reason = EXCLUDED.blocked_reason,
			updated_at     = now()
		RETURNING id
	`, item.ID, item.SourceID, item.ExternalID, item.Title, item.Description, item.Price, item.Address, item.BlockedReason)
	if err != nil {
		return domain.Item{}, fmt.Errorf("upsert items: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return domain.Item{}, fmt.Errorf("upsert items: %w", err)
		}
		return domain.Item{}, fmt.Errorf("upsert items: no row returned (unexpected — INSERT..ON CONFLICT..RETURNING always returns exactly one)")
	}
	if err := rows.Scan(&item.ID); err != nil {
		return domain.Item{}, fmt.Errorf("upsert items: scan id: %w", err)
	}
	return item, rows.Err()
}
