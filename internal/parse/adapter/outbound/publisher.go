package outbound

import (
	"context"
	"encoding/json"

	"buildscrape/internal/core"
	"buildscrape/internal/platform/queue"
	shared "buildscrape/internal/shared/contracts"

	"github.com/google/uuid"
)

// AMQPPublisher реализует parse/domain.NextStagePublisher +
// parse/domain.EventPublisher.
type AMQPPublisher struct {
	conn queue.Client
}

func NewAMQPPublisher(c queue.Client) *AMQPPublisher {
	return &AMQPPublisher{conn: c}
}

func (p *AMQPPublisher) envelope(itemID uuid.UUID) shared.Envelope {
	return shared.Envelope{ItemID: itemID, PipelineVersion: core.CurrentPipelineVersion}
}

func (p *AMQPPublisher) PublishMediaJob(ctx context.Context, itemID uuid.UUID, photoURLs []string) error {
	body, err := json.Marshal(shared.MediaJob{Envelope: p.envelope(itemID), PhotoURLs: photoURLs})
	if err != nil {
		return err
	}
	return p.conn.Publish(ctx, "media.exchange", "", body) // direct exchange
}

func (p *AMQPPublisher) PublishGeocodeJob(ctx context.Context, itemID uuid.UUID, address string) error {
	body, err := json.Marshal(shared.GeocodeJob{Envelope: p.envelope(itemID), Address: address})
	if err != nil {
		return err
	}
	return p.conn.Publish(ctx, "geocode.exchange", "", body)
}

func (p *AMQPPublisher) PublishAIJob(ctx context.Context, itemID uuid.UUID, description string) error {
	body, err := json.Marshal(shared.AIJob{Envelope: p.envelope(itemID), Description: description})
	if err != nil {
		return err
	}
	return p.conn.Publish(ctx, "ai.exchange", "", body)
}

func (p *AMQPPublisher) PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error {
	body, err := json.Marshal(shared.ItemProcessedEvent{
		Envelope: p.envelope(itemID), Stage: stage, Status: status, Error: errMsg,
	})
	if err != nil {
		return err
	}
	return p.conn.Publish(ctx, "pipeline.events", "", body) // fanout — routing key игнорируется
}
