package inbound

import (
	"context"
	"encoding/json"
	"time"

	"buildscrape/internal/fetch/domain"
	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
	shared "buildscrape/internal/shared/contracts"
	shareddomain "buildscrape/internal/shared/domain"
)

// AMQPConsumer — F1b: читает fetch.queue, скачивает страницу. Именно эту
// очередь ускоряет `--scale fetch-worker=N` (CLAUDE.md §4.1/roadmap п.2).
//
// Единственная стадия, где IncProcessed/ObserveDuration вызываются прямо
// здесь, а не через shared/adapter/outbound.EventRecorder: у Fetch нет
// EventRecorder вообще (FK pipeline_events.item_id → items.id, MODEL
// §1.5bis) — оба метода живут в одном месте именно по этой причине.
type AMQPConsumer struct {
	svc      domain.Service
	conn     queue.Client
	recorder observability.Recorder
}

func NewAMQPConsumer(c queue.Client, svc domain.Service, recorder observability.Recorder) *AMQPConsumer {
	return &AMQPConsumer{conn: c, svc: svc, recorder: recorder}
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
		start := time.Now()
		err := c.svc.FetchOne(ctx, job.SourceID, job.URL, job.ExternalID)
		c.recorder.ObserveDuration("fetch", time.Since(start).Seconds())
		if err != nil {
			// 429/5xx/сеть/нет прокси — backoff 1/2/4 мин; прочие 4xx
			// (ErrPermanent) — сразу в fetch.dlq.
			c.recorder.IncProcessed("fetch", "error")
			d.Fail(ctx, shareddomain.IsPermanent(err))
			continue
		}
		c.recorder.IncProcessed("fetch", "done")
		_ = d.Ack()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return queue.ErrDeliveriesClosed
}
