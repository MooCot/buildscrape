//go:build e2e

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	fetchadapteroutbound "buildscrape/internal/fetch/adapter/outbound"

	"github.com/google/uuid"
)

// TestClaimDue_ConcurrentSchedulersClaimExactlyOnce — E2E-2
// (MODEL-buildscrape.md §1.1bis, C9/P6 — не CLAUDE.md/код, те вторичны
// относительно модели после правки 2026-10-16). `--scale worker-fetch=N`
// запускает F1a в N параллельных инстансах одновременно (CLAUDE.md §4.1);
// C9 требует, чтобы готовый к опросу `Source` получил ровно один из них,
// не больше. Фейковый db.Pool в юнит-тестах не воспроизводит `ON CONFLICT
// ... WHERE` под блокировкой строки реального Postgres — отсюда e2e, не
// unit.
func TestClaimDue_ConcurrentSchedulersClaimExactlyOnce(t *testing.T) {
	pool := setupPostgres(t)
	ctx := context.Background()

	sourceID := insertSource(t, ctx, pool) // общий helper, parse_upsert_test.go
	scheduler := fetchadapteroutbound.NewPostgresSourceScheduler(pool)
	now := time.Now()

	const concurrency = 10 // тот же порядок, что --scale worker-fetch=10 в живой демонстрации сессии
	var wg sync.WaitGroup
	claims := make([][]uuid.UUID, concurrency)
	errs := make([]error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			due, err := scheduler.ClaimDue(ctx, now)
			if err != nil {
				errs[i] = err
				return
			}
			for _, s := range due {
				claims[i] = append(claims[i], s.ID)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: ClaimDue: %v", i, err)
		}
	}

	var winners int
	for i, c := range claims {
		if len(c) == 0 {
			continue
		}
		winners++
		if len(c) > 1 || c[0] != sourceID {
			t.Errorf("goroutine %d claimed unexpected result: %v", i, c)
		}
	}
	if winners != 1 {
		t.Errorf("C9 violated: expected exactly 1 of %d concurrent ClaimDue callers to win, got %d", concurrency, winners)
	}
}
