//go:build e2e

package e2e

import (
	"context"
	"testing"

	parseadapteroutbound "buildscrape/internal/parse/adapter/outbound"
	parsedomain "buildscrape/internal/parse/domain"

	"github.com/google/uuid"
)

// TestParseUpsert_BlockedRefetchDoesNotClobberGoodContent — E2E-4
// (MODEL-buildscrape.md §1.3 "Важное исправление" 2026-10-16, C10/P7).
// Другой писатель (F2 сам), другая грань того же гейта A, чем C3 (которая
// про F3/F4/F5 не писать после блокировки) — здесь про то, что САМ upsert
// не теряет уже хороший контент, когда новый фетч оказался заглушкой/
// CAPTCHA. Fake db.Pool в юнит-тестах не воспроизводит реальный `CASE
// WHEN ... THEN ... ELSE ... END` внутри `ON CONFLICT` — нужен настоящий
// Postgres.
func TestParseUpsert_BlockedRefetchDoesNotClobberGoodContent(t *testing.T) {
	pool := setupPostgres(t)
	ctx := context.Background()

	sourceID := insertSource(t, ctx, pool)
	writer := parseadapteroutbound.NewPostgresItemWriter(pool)

	good := parsedomain.Item{
		ID:          uuid.New(),
		SourceID:    sourceID,
		ExternalID:  "99",
		Title:       "Кирпичный дом, 150 м²",
		Description: "Хороший дом у реки",
		Price:       2500000,
		Address:     "ул. Садовая, 5",
	}
	if _, err := writer.Upsert(ctx, good); err != nil {
		t.Fatalf("initial good upsert: %v", err)
	}

	blocked := "captcha"
	stale := parsedomain.Item{
		ID:            good.ID,
		SourceID:      sourceID,
		ExternalID:    good.ExternalID,
		Title:         "",
		Description:   "",
		Price:         0,
		Address:       "",
		BlockedReason: &blocked,
	}
	result, err := writer.Upsert(ctx, stale)
	if err != nil {
		t.Fatalf("blocked upsert: %v", err)
	}

	if result.BlockedReason == nil || *result.BlockedReason != blocked {
		t.Errorf("expected blocked_reason to be set to %q, got %v", blocked, result.BlockedReason)
	}

	var title, description, address string
	var price float64
	rows, err := pool.Query(ctx,
		`SELECT title, description, price, address FROM items WHERE source_id = $1 AND external_id = $2`,
		sourceID, good.ExternalID,
	)
	if err != nil {
		t.Fatalf("select item: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("select item: no row returned")
	}
	if err := rows.Scan(&title, &description, &price, &address); err != nil {
		t.Fatalf("select item: scan: %v", err)
	}

	if title != good.Title {
		t.Errorf("title clobbered: got %q, want %q", title, good.Title)
	}
	if description != good.Description {
		t.Errorf("description clobbered: got %q, want %q", description, good.Description)
	}
	if price != good.Price {
		t.Errorf("price clobbered: got %v, want %v", price, good.Price)
	}
	if address != good.Address {
		t.Errorf("address clobbered: got %q, want %q", address, good.Address)
	}
}
