// Package inbound (media) — CPU/RAM-тяжёлая стадия, отдельный тип воркера
// (CLAUDE.md §3.3) — низкий prefetch намеренно (ТЕОРИЯ-УГЛУБЛЁННО.md §2.4).
package inbound

import (
	"context"
	"encoding/json"

	"buildscrape/internal/media/domain"
	"buildscrape/internal/platform/queue"
	shared "buildscrape/internal/shared/contracts"
	shareddomain "buildscrape/internal/shared/domain"
)

type AMQPConsumer struct {
	svc  domain.Service
	conn queue.Client
}

func NewAMQPConsumer(c queue.Client, svc domain.Service) *AMQPConsumer {
	return &AMQPConsumer{conn: c, svc: svc}
}

func (c *AMQPConsumer) Run(ctx context.Context) error {
	deliveries, err := c.conn.Consume(ctx, "media.queue", 1) // низкий prefetch — тяжёлая обработка
	if err != nil {
		return err
	}
	for d := range deliveries {
		var job shared.MediaJob
		if err := json.Unmarshal(d.Body, &job); err != nil {
			_ = d.Nack(false) // poison pill → DLQ, не requeue
			continue
		}
		if err := c.svc.ProcessItem(ctx, job.ItemID, job.PhotoURLs); err != nil {
			d.Fail(ctx, shareddomain.IsPermanent(err)) // backoff → DLQ после лимита
			continue
		}
		_ = d.Ack()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return queue.ErrDeliveriesClosed
}
