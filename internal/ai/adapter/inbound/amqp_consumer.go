package inbound

import (
	"context"
	"encoding/json"

	"buildscrape/internal/ai/domain"
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
	deliveries, err := c.conn.Consume(ctx, "ai.queue", 1)
	if err != nil {
		return err
	}
	for d := range deliveries {
		var job shared.AIJob
		if err := json.Unmarshal(d.Body, &job); err != nil {
			_ = d.Nack(false)
			continue
		}
		if err := c.svc.ClassifyItem(ctx, job.ItemID, job.Description); err != nil {
			// ErrAllProvidersUnavailable — временный сбой по определению
			// (breaker сам восстановится, ТЕОРИЯ-УГЛУБЛЁННО.md §8.1) →
			// backoff через wait-очередь, не DLQ сразу и не мгновенный
			// requeue (пока breaker открыт, тот крутился бы вхолостую).
			d.Fail(ctx, shareddomain.IsPermanent(err))
			continue
		}
		_ = d.Ack()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return queue.ErrDeliveriesClosed
}
