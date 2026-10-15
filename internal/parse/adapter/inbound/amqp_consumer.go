// Package inbound (parse) — читает parse.queue, вызывает domain.Service,
// manual ack/nack (ТЕОРИЯ-УГЛУБЛЁННО.md §2.2).
package inbound

import (
	"context"
	"encoding/json"

	"buildscrape/internal/parse/domain"
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
	deliveries, err := c.conn.Consume(ctx, "parse.queue", 1) // DefaultPrefetchCount, internal/core
	if err != nil {
		return err
	}
	for d := range deliveries {
		var job shared.ParseJob
		if err := json.Unmarshal(d.Body, &job); err != nil {
			// malformed payload — poison pill, не временный сбой: сразу в
			// parse.dlq (через DLX), не requeue (ТЕОРИЯ-УГЛУБЛЁННО.md §6.4).
			_ = d.Nack(false)
			continue
		}
		if err := c.svc.Parse(ctx, job.RawHTML, job.SourceID, job.ItemID, job.ExternalID); err != nil {
			// Невалидная карточка (ErrPermanent) — сразу в DLQ; всё остальное
			// (БД недоступна и т.п.) — backoff 1/2/4 мин, после
			// MaxRetryAttempts — тоже в DLQ (queue.Delivery.Fail).
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
