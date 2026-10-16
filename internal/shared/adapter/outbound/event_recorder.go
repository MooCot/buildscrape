package outbound

import (
	"context"
	"encoding/json"
	"log"

	"buildscrape/internal/core"
	"buildscrape/internal/platform/db"
	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
	shared "buildscrape/internal/shared/contracts"

	"github.com/google/uuid"
)

// EventRecorder реализует PublishStageEvent (Parse/Media/Geocode/AI —
// parse/domain.EventPublisher, media/domain.EventPublisher, etc., все с
// идентичной сигнатурой) ОДИН раз, не по копии на стадию. Раньше каждая
// стадия сама публиковала ItemProcessedEvent в pipeline.events (fanout) и
// этим ограничивалась — MODEL-buildscrape.md §1.5 "пишут все предметы"
// подразумевало прямую запись в таблицу `pipeline_events`, а fanout без
// привязанной очереди молча роняет сообщения (никто её не слушал). Правка
// MODEL §1.5/§9 (2026-10-16): INSERT в pipeline_events — обязательная,
// синхронная часть результата стадии; публикация в pipeline.events для
// живого дашборда — best-effort рядом, не вместо. Третья обязанность
// (добавлена тем же днём, CLAUDE.md §10.2) — IncProcessed(stage, status) в
// той же точке, тем же (stage, status), что уже идёт в журнал: один call
// site, метрика и журнал не могут разойтись.
type EventRecorder struct {
	pool     db.Pool
	conn     queue.Client
	recorder observability.Recorder
}

func NewEventRecorder(pool db.Pool, conn queue.Client, recorder observability.Recorder) *EventRecorder {
	return &EventRecorder{pool: pool, conn: conn, recorder: recorder}
}

func (r *EventRecorder) PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error {
	var errArg any
	if errMsg != "" {
		errArg = errMsg
	}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO pipeline_events (item_id, stage, status, error) VALUES ($1, $2, $3, $4)
	`, itemID, stage, status, errArg); err != nil {
		return err // журнал обязателен (MODEL §1.5) — ошибка здесь = ошибка всей стадии, не глушится
	}
	r.recorder.IncProcessed(stage, status)

	body, err := json.Marshal(shared.ItemProcessedEvent{
		Envelope: shared.Envelope{ItemID: itemID, PipelineVersion: core.CurrentPipelineVersion},
		Stage:    stage, Status: status, Error: errMsg,
	})
	if err != nil {
		log.Printf("pipeline.events marshal (best-effort, live dashboard only): %v", err)
		return nil // журнал уже записан — это не причина ретраить стадию
	}
	if err := r.conn.Publish(ctx, "pipeline.events", "", body); err != nil {
		log.Printf("pipeline.events publish (best-effort, live dashboard only): %v", err)
	}
	return nil
}
