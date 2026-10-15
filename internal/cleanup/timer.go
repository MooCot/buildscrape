package cleanup

import (
	"context"
	"time"

	"buildscrape/internal/platform/db"
)

// Timer — cron/systemd timer, НЕ consumer очереди (CLAUDE.md §3.7).
type Timer struct {
	pool     db.Pool
	ttl      time.Duration
	interval time.Duration
}

func NewTimer(pool db.Pool, ttl, interval time.Duration) *Timer {
	return &Timer{pool: pool, ttl: ttl, interval: interval}
}

func (t *Timer) Run(ctx context.Context) error {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err() // graceful shutdown, ТЕОРИЯ-УГЛУБЛЁННО.md §8.3
		case <-ticker.C:
			if _, err := Purge(ctx, t.pool, t.ttl); err != nil {
				_ = err // TODO: structured log + Prometheus counter
			}
		}
	}
}
