package inbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
	"buildscrape/internal/platform/queue/queuetest"
	shared "buildscrape/internal/shared/contracts"
	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

type fakeSvc struct {
	err   error
	calls []call
}

type call struct {
	rawHTML         string
	sourceID        uuid.UUID
	candidateItemID uuid.UUID
	externalID      string
}

func (s *fakeSvc) Parse(ctx context.Context, rawHTML string, sourceID, candidateItemID uuid.UUID, externalID string) error {
	s.calls = append(s.calls, call{rawHTML, sourceID, candidateItemID, externalID})
	return s.err
}

func run(t *testing.T, body []byte, svc *fakeSvc, retryErr error) *queuetest.Recorder {
	t.Helper()
	d, rec := queuetest.NewDelivery(body)
	rec.RetryErr = retryErr
	c := NewAMQPConsumer(&queuetest.Client{Deliveries: []queue.Delivery{d}}, svc, observability.NewNoopRecorder())
	// queuetest.Client закрывает канал после подготовленных сообщений при
	// живом ctx — для consumer'а это "оборвалось соединение": он обязан
	// вернуть ErrDeliveriesClosed, а не nil (иначе воркер вышел бы с кодом 0).
	if err := c.Run(context.Background()); !errors.Is(err, queue.ErrDeliveriesClosed) {
		t.Fatalf("Run: %v, want ErrDeliveriesClosed", err)
	}
	return rec
}

func validJob(t *testing.T) (shared.ParseJob, []byte) {
	t.Helper()
	job := shared.ParseJob{
		Envelope:   shared.Envelope{ItemID: uuid.New(), PipelineVersion: "1.0.0"},
		SourceID:   uuid.New(),
		ExternalID: "ext-42",
		RawHTML:    "<html/>",
	}
	body, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	return job, body
}

func TestConsumer_Success_Acks(t *testing.T) {
	job, body := validJob(t)
	svc := &fakeSvc{}
	rec := run(t, body, svc, nil)

	if rec.Acks != 1 || rec.Retries != 0 || len(rec.Nacks) != 0 {
		t.Errorf("acks=%d retries=%d nacks=%v, want 1/0/[]", rec.Acks, rec.Retries, rec.Nacks)
	}
	// Регрессия: раньше consumer передавал job.ItemID как externalID, а
	// настоящего ExternalID в контракте не было вовсе.
	if len(svc.calls) != 1 {
		t.Fatalf("svc.calls=%d, want 1", len(svc.calls))
	}
	got := svc.calls[0]
	if got.externalID != "ext-42" {
		t.Errorf("externalID=%q, want ext-42 (не ItemID)", got.externalID)
	}
	if got.candidateItemID != job.ItemID || got.sourceID != job.SourceID || got.rawHTML != job.RawHTML {
		t.Errorf("поля job не дошли до сервиса как есть: %+v", got)
	}
}

func TestConsumer_MalformedBody_NacksWithoutRequeue(t *testing.T) {
	svc := &fakeSvc{}
	rec := run(t, []byte("{not json"), svc, nil)

	// poison pill — временным не бывает, retry бессмысленен: сразу в DLQ.
	if len(rec.Nacks) != 1 || rec.Nacks[0] != false {
		t.Errorf("nacks=%v, want [false]", rec.Nacks)
	}
	if rec.Retries != 0 || rec.Acks != 0 || len(svc.calls) != 0 {
		t.Errorf("retries=%d acks=%d svc.calls=%d, want 0/0/0", rec.Retries, rec.Acks, len(svc.calls))
	}
}

func TestConsumer_ServiceError_RetriesInsteadOfImmediateRequeue(t *testing.T) {
	_, body := validJob(t)
	rec := run(t, body, &fakeSvc{err: errors.New("db down")}, nil)

	if rec.Retries != 1 {
		t.Errorf("retries=%d, want 1", rec.Retries)
	}
	if len(rec.Nacks) != 0 || rec.Acks != 0 {
		t.Errorf("nacks=%v acks=%d, want none — мгновенный Nack(true) заменён backoff-retry", rec.Nacks, rec.Acks)
	}
}

func TestConsumer_InvalidListing_GoesStraightToDLQ(t *testing.T) {
	_, body := validJob(t)
	invalid := fmt.Errorf("%w: invalid listing: missing/empty title", shareddomain.ErrPermanent)
	rec := run(t, body, &fakeSvc{err: invalid}, nil)

	// Невалидная карточка (CLAUDE.md §3.2) — в parse.dlq сразу, а не через
	// три круга ожидания (1+2+4 мин) ради заведомо того же результата.
	// DeadLetter, не Nack — тот не даёт приложить x-last-error.
	if rec.DeadLetters != 1 || rec.Retries != 0 || len(rec.Nacks) != 0 {
		t.Errorf("deadLetters=%d retries=%d nacks=%v, want 1/0/[]", rec.DeadLetters, rec.Retries, rec.Nacks)
	}
}

func TestConsumer_ServiceError_RetryBroken_FallsBackToRequeue(t *testing.T) {
	_, body := validJob(t)
	rec := run(t, body, &fakeSvc{err: errors.New("db down")}, errors.New("publish to retry queue nacked"))

	if rec.Retries != 1 {
		t.Errorf("retries=%d, want 1", rec.Retries)
	}
	if len(rec.Nacks) != 1 || rec.Nacks[0] != true {
		t.Errorf("nacks=%v, want [true] — при отказе самого retry сообщение не должно остаться неподтверждённым", rec.Nacks)
	}
}
