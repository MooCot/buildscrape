//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	aiadapteroutbound "buildscrape/internal/ai/adapter/outbound"
	aidomain "buildscrape/internal/ai/domain"
)

// TestBreakerStore_OpenedAtSurvivesRestart — E2E-6
// (MODEL-buildscrape.md §1.4bis, C11/P9 — четвёртый случай скрытой
// Сущности в этом документе, тот же класс, что SourcePollState §1.1bis).
// Домен-уровень гидратации уже покрыт юнит-тестом
// (TestClassifyItem_BreakerStateSurvivesRestart, ai/domain/service_test.go,
// fake store) — здесь проверяется именно реальный Postgres-адаптер
// (PostgresBreakerStore): round-trip `opened_at` через NULL/non-NULL
// TIMESTAMPTZ, который fake-store не воспроизводит вообще.
func TestBreakerStore_OpenedAtSurvivesRestart(t *testing.T) {
	pool := setupPostgres(t)
	ctx := context.Background()
	store := aiadapteroutbound.NewPostgresBreakerStore(pool)

	t.Run("Open с opened_at — переживает рестарт (новый store поверх той же БД)", func(t *testing.T) {
		openedAt := time.Now().Add(-10 * time.Second).Truncate(time.Microsecond)
		if err := store.Save(ctx, "p-open", aidomain.BreakerSnapshot{
			State: aidomain.Open, OpenedAt: openedAt,
		}); err != nil {
			t.Fatalf("save: %v", err)
		}

		// "рестарт процесса" — новый store (как новый *PostgresBreakerStore
		// после перезапуска воркера), та же БД.
		revived := aiadapteroutbound.NewPostgresBreakerStore(pool)
		snap, err := revived.Load(ctx, "p-open")
		if err != nil {
			t.Fatalf("load after restart: %v", err)
		}
		if snap.State != aidomain.Open {
			t.Errorf("state = %v, want Open", snap.State)
		}
		if !snap.OpenedAt.Equal(openedAt) {
			t.Errorf("C11 violated: opened_at = %v, want %v (не обнулился после рестарта)", snap.OpenedAt, openedAt)
		}
	})

	t.Run("провайдер никогда не сохранялся — zero-value без ошибки (P9, NULL-ветка)", func(t *testing.T) {
		snap, err := store.Load(ctx, "never-seen-provider")
		if err != nil {
			t.Fatalf("load for unknown provider: %v", err)
		}
		if snap.State != aidomain.Closed {
			t.Errorf("state = %v, want zero-value Closed", snap.State)
		}
		if !snap.OpenedAt.IsZero() {
			t.Errorf("opened_at = %v, want zero (never opened)", snap.OpenedAt)
		}
	})
}
