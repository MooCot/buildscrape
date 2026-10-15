package outbound

import (
	"context"
	"encoding/json"

	"buildscrape/internal/core"
	"buildscrape/internal/platform/queue"
	shared "buildscrape/internal/shared/contracts"

	"github.com/google/uuid"
)

type AMQPEventPublisher struct {
	conn queue.Client
}

func NewAMQPEventPublisher(c queue.Client) *AMQPEventPublisher {
	return &AMQPEventPublisher{conn: c}
}

func (p *AMQPEventPublisher) PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error {
	body, err := json.Marshal(shared.ItemProcessedEvent{
		Envelope: shared.Envelope{ItemID: itemID, PipelineVersion: core.CurrentPipelineVersion},
		Stage:    stage, Status: status, Error: errMsg,
	})
	if err != nil {
		return err
	}
	return p.conn.Publish(ctx, "pipeline.events", "", body)
}
