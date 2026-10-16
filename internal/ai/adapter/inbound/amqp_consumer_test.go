package inbound

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	aidomain "buildscrape/internal/ai/domain"
	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
	"buildscrape/internal/platform/queue/queuetest"
	shared "buildscrape/internal/shared/contracts"

	"github.com/google/uuid"
)

type fakeSvc struct {
	err          error
	itemIDs      []uuid.UUID
	descriptions []string
}

func (s *fakeSvc) ClassifyItem(ctx context.Context, itemID uuid.UUID, description string) error {
	s.itemIDs = append(s.itemIDs, itemID)
	s.descriptions = append(s.descriptions, description)
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

func validJob(t *testing.T) (shared.AIJob, []byte) {
	t.Helper()
	job := shared.AIJob{
		Envelope:    shared.Envelope{ItemID: uuid.New(), PipelineVersion: "1.0.0"},
		Description: "3-room flat, renovated",
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
	if len(svc.itemIDs) != 1 || svc.itemIDs[0] != job.ItemID || svc.descriptions[0] != job.Description {
		t.Errorf("поля job не дошли до сервиса как есть: ids=%v desc=%v", svc.itemIDs, svc.descriptions)
	}
}

func TestConsumer_MalformedBody_NacksWithoutRequeue(t *testing.T) {
	svc := &fakeSvc{}
	rec := run(t, []byte("{not json"), svc, nil)

	if len(rec.Nacks) != 1 || rec.Nacks[0] != false {
		t.Errorf("nacks=%v, want [false]", rec.Nacks)
	}
	if rec.Retries != 0 || rec.Acks != 0 || len(svc.itemIDs) != 0 {
		t.Errorf("retries=%d acks=%d svc.calls=%d, want 0/0/0", rec.Retries, rec.Acks, len(svc.itemIDs))
	}
}

// Главный сценарий именно для AI: пока breaker открыт, мгновенный requeue
// крутился бы вхолостую (ТЕОРИЯ-УГЛУБЛЁННО.md §2.6) — сообщение обязано
// уйти в backoff.
func TestConsumer_AllProvidersUnavailable_BacksOffInsteadOfSpinning(t *testing.T) {
	_, body := validJob(t)
	rec := run(t, body, &fakeSvc{err: aidomain.ErrAllProvidersUnavailable}, nil)

	if rec.Retries != 1 || len(rec.Nacks) != 0 || rec.Acks != 0 {
		t.Errorf("retries=%d nacks=%v acks=%d, want 1/[]/0", rec.Retries, rec.Nacks, rec.Acks)
	}
}

func TestConsumer_ServiceError_RetryBroken_FallsBackToRequeue(t *testing.T) {
	_, body := validJob(t)
	rec := run(t, body, &fakeSvc{err: errors.New("boom")}, errors.New("retry publish nacked"))

	if len(rec.Nacks) != 1 || rec.Nacks[0] != true {
		t.Errorf("nacks=%v, want [true]", rec.Nacks)
	}
}
