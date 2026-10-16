package inbound

import (
	"context"
	"encoding/json"
	"time"

	"buildscrape/internal/ai/domain"
	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
	shared "buildscrape/internal/shared/contracts"
	shareddomain "buildscrape/internal/shared/domain"
)

type AMQPConsumer struct {
	svc      domain.Service
	conn     queue.Client
	recorder observability.Recorder
}

func NewAMQPConsumer(c queue.Client, svc domain.Service, recorder observability.Recorder) *AMQPConsumer {
	return &AMQPConsumer{conn: c, svc: svc, recorder: recorder}
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
		start := time.Now()
		err := c.svc.ClassifyItem(ctx, job.ItemID, job.Description)
		c.recorder.ObserveDuration("ai", time.Since(start).Seconds())
		if err != nil {
			// ErrAllProvidersUnavailable — временный сбой по определению
			// (breaker сам восстановится, ТЕОРИЯ-УГЛУБЛЁННО.md §8.1) →
			// backoff через wait-очередь, не DLQ сразу и не мгновенный
			// requeue (пока breaker открыт, тот крутился бы вхолостую).
			d.Fail(ctx, shareddomain.IsPermanent(err), err.Error())
			continue
		}
		_ = d.Ack()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return queue.ErrDeliveriesClosed
}
