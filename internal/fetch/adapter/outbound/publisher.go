package outbound

import (
	"context"
	"encoding/json"

	"buildscrape/internal/core"
	shared "buildscrape/internal/shared/contracts"

	"github.com/google/uuid"

	"buildscrape/internal/platform/queue"
)

// AMQPPublisher реализует fetch/domain.NextStagePublisher — здесь, и только
// здесь, доменные поля превращаются в contracts.ParseJob (anti-corruption
// слой на выходе из стадии, зеркально input-стороне в adapter/inbound
// других стадий).
type AMQPPublisher struct {
	conn queue.Client
}

func NewAMQPPublisher(c queue.Client) *AMQPPublisher {
	return &AMQPPublisher{conn: c}
}

func (p *AMQPPublisher) PublishParseJob(ctx context.Context, rawItemID, sourceID uuid.UUID, externalID, rawHTML string) error {
	job := shared.ParseJob{
		Envelope:   shared.Envelope{ItemID: rawItemID, PipelineVersion: core.CurrentPipelineVersion},
		SourceID:   sourceID,
		ExternalID: externalID,
		RawHTML:    rawHTML,
	}
	body, err := json.Marshal(job)
	if err != nil {
		return err
	}
	// routing key parse.source.<sourceID> — topic exchange, CLAUDE.md §4.3,
	// позволяет в будущем маршрутизировать по источнику без правки продюсера.
	// .String() здесь — routing key плоский текст (AMQP), не JSON; в самом
	// сообщении sourceID остаётся uuid.UUID (см. contracts.ParseJob).
	return p.conn.Publish(ctx, "parse.exchange", "parse.source."+sourceID.String(), body)
}

// PublishFetchJob — F1a → F1b: задание "скачай эту страницу" в fetch.queue
// (direct exchange, routing key пуст — как у media/geocode/ai).
func (p *AMQPPublisher) PublishFetchJob(ctx context.Context, sourceID uuid.UUID, url, externalID string) error {
	body, err := json.Marshal(shared.FetchJob{
		PipelineVersion: core.CurrentPipelineVersion,
		SourceID:        sourceID,
		URL:             url,
		ExternalID:      externalID,
	})
	if err != nil {
		return err
	}
	return p.conn.Publish(ctx, "fetch.exchange", "", body)
}
