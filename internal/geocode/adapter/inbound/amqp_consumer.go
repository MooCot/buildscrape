package inbound

import (
	"context"
	"encoding/json"
	"time"

	"buildscrape/internal/geocode/domain"
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
	deliveries, err := c.conn.Consume(ctx, "geocode.queue", 1)
	if err != nil {
		return err
	}
	for d := range deliveries {
		var job shared.GeocodeJob
		if err := json.Unmarshal(d.Body, &job); err != nil {
			_ = d.Nack(false)
			continue
		}
		start := time.Now()
		err := c.svc.GeocodeItem(ctx, job.ItemID, job.Address)
		c.recorder.ObserveDuration("geocode", time.Since(start).Seconds())
		if err != nil {
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
