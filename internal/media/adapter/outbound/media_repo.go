package outbound

import (
	"context"

	"buildscrape/internal/media/domain"
	"buildscrape/internal/platform/db"
)

type PostgresMediaWriter struct {
	pool db.Pool
}

func NewPostgresMediaWriter(p db.Pool) *PostgresMediaWriter {
	return &PostgresMediaWriter{pool: p}
}

// Save — чистый INSERT (не upsert): у item_media нет уникального ключа на
// (item_id, kind, ...) — по дизайну несколько строк одного item/kind
// легитимны (несколько исходных фото, каждая photoURL/kind — свой ключ,
// см. media/domain/service.go). Повторная обработка того же item (retry
// после сбоя ДО этой точки) создаёт дополнительные строки — известный,
// не исправленный здесь пробел: идемпотентность Media в целом не входит в
// этот инкремент (TODO на будущее — например, DELETE старых записей item'а
// перед перезаписью, или UNIQUE(item_id, s3_key) + ON CONFLICT DO NOTHING).
func (w *PostgresMediaWriter) Save(ctx context.Context, m domain.ItemMedia) error {
	_, err := w.pool.Exec(ctx, `
		INSERT INTO item_media (id, item_id, s3_key, kind)
		VALUES ($1, $2, $3, $4)
	`, m.ID, m.ItemID, m.S3Key, string(m.Kind))
	return err
}
