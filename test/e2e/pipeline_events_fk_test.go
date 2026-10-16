//go:build e2e

package e2e

import (
	"context"
	"testing"

	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
	sharedadapteroutbound "buildscrape/internal/shared/adapter/outbound"

	"github.com/google/uuid"
)

// TestEventRecorder_FKBoundary — E2E-5 (MODEL-buildscrape.md §1.5: pipeline_events.item_id
// — NOT NULL REFERENCES items(id); уже верная цитата, без правки модели в
// этот раз). Единственный из e2e-тестов, трогающий EventRecorder/
// pipeline_events — E2E-1/2/4 проверяют другие writer'ы. Fake db.Pool в
// юнит-тестах не воспроизводит реальное FK-нарушение Postgres — только
// живая БД может подтвердить, что исключение Fetch/Storage из §1.5bis
// опирается на реальное ограничение, а не на предположение в
// документации.
func TestEventRecorder_FKBoundary(t *testing.T) {
	pool := setupPostgres(t)
	ctx := context.Background()
	rec := sharedadapteroutbound.NewEventRecorder(pool, queue.NewNoopClient(), observability.NewNoopRecorder())

	t.Run("Item exists — успех, строка появилась", func(t *testing.T) {
		sourceID := insertSource(t, ctx, pool)
		itemID := uuid.New()
		if _, err := pool.Exec(ctx,
			`INSERT INTO items (id, source_id, external_id, title, price) VALUES ($1, $2, $3, $4, $5)`,
			itemID, sourceID, "e2e-fk", "Дом", 1000000,
		); err != nil {
			t.Fatalf("insert item: %v", err)
		}

		if err := rec.PublishStageEvent(ctx, itemID, "parse", "done", ""); err != nil {
			t.Fatalf("PublishStageEvent against an existing item: %v", err)
		}

		rows, err := pool.Query(ctx,
			`SELECT stage, status FROM pipeline_events WHERE item_id = $1`,
			itemID,
		)
		if err != nil {
			t.Fatalf("select pipeline_events: %v", err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("expected one pipeline_events row, got none")
		}
		var stage, status string
		if err := rows.Scan(&stage, &status); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if stage != "parse" || status != "done" {
			t.Errorf("got stage=%q status=%q, want parse/done", stage, status)
		}
	})

	t.Run("Item НЕ существует — FK реально отклоняет (не предположение документации)", func(t *testing.T) {
		nonExistentItemID := uuid.New()

		err := rec.PublishStageEvent(ctx, nonExistentItemID, "fetch", "done", "")
		if err == nil {
			t.Fatal("expected a foreign key violation when item_id doesn't exist in items, got nil error — §1.5bis's exclusion of Fetch would be unenforced")
		}
	})
}
