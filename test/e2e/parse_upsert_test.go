//go:build e2e

package e2e

import (
	"context"
	"testing"

	"buildscrape/internal/platform/db"

	parseadapteroutbound "buildscrape/internal/parse/adapter/outbound"
	parsedomain "buildscrape/internal/parse/domain"

	"github.com/google/uuid"
)

// insertSource — items/raw_items.source_id — FK на sources(id)
// (migrations/0001_init.sql); нужна хотя бы одна строка перед upsert'ом.
func insertSource(t *testing.T, ctx context.Context, pool db.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO sources (id, name, base_url) VALUES ($1, $2, $3)`,
		id, "e2e-source", "http://example.invalid",
	); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	return id
}

func countItems(t *testing.T, ctx context.Context, pool db.Pool, sourceID uuid.UUID, externalID string) int {
	t.Helper()
	rows, err := pool.Query(ctx,
		`SELECT count(*) FROM items WHERE source_id = $1 AND external_id = $2`,
		sourceID, externalID,
	)
	if err != nil {
		t.Fatalf("count items: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("count items: no row returned")
	}
	var n int
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("count items: scan: %v", err)
	}
	return n
}

// TestParseUpsert_RedeliveredMessageIsIdempotent — E2E-1 (test/e2e/README.md,
// назван там "первый кандидат"; реализует C1/C2/P4, MODEL-buildscrape.md
// §10; ТЕОРИЯ-УГЛУБЛЁННО.md §6.2). AMQP — at-least-once: то же ParseJob
// может прийти дважды (сбой до ack, ручной requeue). Fake db.Pool в юнит-
// тестах не воспроизводит `ON CONFLICT` реального Postgres — здесь
// проверяется именно он, не доменная логика вокруг него (та уже покрыта
// юнит-тестами на фейках в internal/parse/...).
func TestParseUpsert_RedeliveredMessageIsIdempotent(t *testing.T) {
	pool := setupPostgres(t)
	ctx := context.Background()

	sourceID := insertSource(t, ctx, pool)
	writer := parseadapteroutbound.NewPostgresItemWriter(pool)

	item := parsedomain.Item{
		ID:         uuid.New(), // candidateID от Fetch, CLAUDE.md §4.4 — сквозной через весь pipeline
		SourceID:   sourceID,
		ExternalID: "42",
		Title:      "Дом, 100 м²",
		Price:      1000000,
	}

	first, err := writer.Upsert(ctx, item)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Повторная доставка ТОГО ЖЕ сообщения — AMQP at-least-once, не
	// гипотетический сценарий.
	second, err := writer.Upsert(ctx, item)
	if err != nil {
		t.Fatalf("redelivered upsert: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("redelivery changed item id: %s -> %s — RETURNING обязан сохранить id первой вставки при конфликте (item_repo.go doc-comment)", first.ID, second.ID)
	}
	if n := countItems(t, ctx, pool, sourceID, item.ExternalID); n != 1 {
		t.Errorf("expected exactly 1 row for (source_id, external_id), got %d — redelivery must not duplicate", n)
	}
}
