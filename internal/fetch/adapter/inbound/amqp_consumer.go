package inbound

import (
	"context"
	"encoding/json"

	"buildscrape/internal/fetch/domain"
	"buildscrape/internal/platform/queue"
	shared "buildscrape/internal/shared/contracts"
	shareddomain "buildscrape/internal/shared/domain"
)

// AMQPConsumer — F1b: читает fetch.queue, скачивает страницу. Именно эту
// очередь ускоряет `--scale fetch-worker=N` (CLAUDE.md §4.1/roadmap п.2).
type AMQPConsumer struct {
	svc  domain.Service
	conn queue.Client
}

func NewAMQPConsumer(c queue.Client, svc domain.Service) *AMQPConsumer {
	return &AMQPConsumer{conn: c, svc: svc}
}

func (c *AMQPConsumer) Run(ctx context.Context) error {
	deliveries, err := c.conn.Consume(ctx, "fetch.queue", 1)
	if err != nil {
		return err
	}
	for d := range deliveries {
		var job shared.FetchJob
		if err := json.Unmarshal(d.Body, &job); err != nil {
			_ = d.Nack(false) // poison pill → fetch.dlq
			continue
		}
		if err := c.svc.FetchOne(ctx, job.SourceID, job.URL, job.ExternalID); err != nil {
			// 429/5xx/сеть/нет прокси — backoff 1/2/4 мин; прочие 4xx
			// (ErrPermanent) — сразу в fetch.dlq.
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
