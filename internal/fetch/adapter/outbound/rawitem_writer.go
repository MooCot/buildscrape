package outbound

import (
	"context"

	"buildscrape/internal/fetch/domain"
	"buildscrape/internal/platform/db"
)

// PostgresRawItemWriter — реализует domain.RawItemWriter через platform/db.
// ON CONFLICT (source_id, external_id) DO UPDATE — дедуп на входе pipeline
// (CLAUDE.md §3.1): повторный опрос того же external_id ОБНОВЛЯЕТ ту же
// строку (свежий HTML, status снова 'fetched' — переоткрывает переобработку),
// не плодит дубликаты. id — НЕ в SET (сохраняется стабильным между
// повторными фетчами, хотя ничто на него не ссылается по FK — дешёвая
// консистентность, тот же принцип, что и в items, см. parse/adapter/
// outbound/item_repo.go).
type PostgresRawItemWriter struct {
	pool db.Pool
}

func NewPostgresRawItemWriter(p db.Pool) *PostgresRawItemWriter {
	return &PostgresRawItemWriter{pool: p}
}

func (w *PostgresRawItemWriter) Save(ctx context.Context, item domain.RawItem) error {
	_, err := w.pool.Exec(ctx, `
		INSERT INTO raw_items (id, source_id, external_id, raw_html, fetched_at, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source_id, external_id) DO UPDATE SET
			raw_html   = EXCLUDED.raw_html,
			fetched_at = EXCLUDED.fetched_at,
			status     = EXCLUDED.status
	`, item.ID, item.SourceID, item.ExternalID, item.RawHTML, item.FetchedAt, string(item.Status))
	return err
}
