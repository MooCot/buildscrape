package main

import (
	"context"
	"log"
	"time"

	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
)

// runQueueLengthPoller — F11/П-metrics (MODEL-buildscrape.md §4/§8,
// CLAUDE.md §10.3). Интервал равен scrape_interval в prometheus.yml (15с)
// — опрашивать чаще не добавляет точности между scrape'ами, только лишние
// round-trip'ы к брокеру. Список очередей — queue.AllQueueNames(Topology),
// не отдельный хардкод. Ошибка одной очереди не останавливает опрос
// остальных и не возвращается наружу (observability — best-effort,
// CLAUDE.md §10.5) — только логируется.
func runQueueLengthPoller(ctx context.Context, q queue.Client, recorder observability.Recorder) {
	names := queue.AllQueueNames(queue.Topology)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, name := range names {
				n, err := q.QueueLength(ctx, name)
				if err != nil {
					log.Printf("metrics poller: queue length %s: %v", name, err)
					continue
				}
				recorder.SetQueueLength(name, n)
			}
		}
	}
}
