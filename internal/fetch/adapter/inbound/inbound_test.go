package inbound

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"buildscrape/internal/platform/observability"
	"buildscrape/internal/platform/queue"
	"buildscrape/internal/platform/queue/queuetest"
	shared "buildscrape/internal/shared/contracts"
	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// --- PollTrigger ---

type fakeScheduler struct {
	calls atomic.Int32
	err   error
}

func (f *fakeScheduler) ScheduleDue(ctx context.Context) error {
	f.calls.Add(1)
	return f.err
}

// waitFor — ждём условие с таймаутом вместо фиксированного Sleep: под -race и
// на загруженной машине тики идут медленнее, и "поспал 60мс и посчитал" —
// тест про планировщик ОС, а не про триггер.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("не дождались: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPollTrigger_TicksImmediatelyThenPeriodically(t *testing.T) {
	s := &fakeScheduler{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	// Интервал 1 час: любой второй вызов мог бы дать только ticker, а не
	// первый немедленный тик — проверяем оба свойства раздельно ниже.
	go func() { done <- NewPollTrigger(s, time.Hour).Run(ctx) }()

	waitFor(t, "немедленный первый тик (не через interval)", func() bool { return s.calls.Load() >= 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run=%v, want context.Canceled", err)
	}
	if n := s.calls.Load(); n != 1 {
		t.Errorf("ScheduleDue вызван %d раз при interval=1ч, want ровно 1 (немедленный тик)", n)
	}
}

func TestPollTrigger_KeepsTickingPeriodically(t *testing.T) {
	s := &fakeScheduler{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewPollTrigger(s, 5*time.Millisecond).Run(ctx) }()

	waitFor(t, "≥3 вызова ScheduleDue", func() bool { return s.calls.Load() >= 3 })
	cancel()
	<-done
}

func TestPollTrigger_ErrorDoesNotStopTheLoop(t *testing.T) {
	s := &fakeScheduler{err: errors.New("db down")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewPollTrigger(s, 5*time.Millisecond).Run(ctx) }()

	// Если бы ошибка планирования останавливала цикл, третьего вызова не было бы.
	waitFor(t, "≥3 вызова несмотря на ошибку каждого", func() bool { return s.calls.Load() >= 3 })
	cancel()
	<-done
}

// --- AMQPConsumer ---

type fakeSvc struct {
	err   error
	calls []fetchCall
}

type fetchCall struct {
	sourceID   uuid.UUID
	url, extID string
}

func (s *fakeSvc) ScheduleDue(ctx context.Context) error { return nil }
func (s *fakeSvc) FetchOne(ctx context.Context, sourceID uuid.UUID, url, externalID string) error {
	s.calls = append(s.calls, fetchCall{sourceID, url, externalID})
	return s.err
}

func run(t *testing.T, body []byte, svc *fakeSvc, retryErr error) *queuetest.Recorder {
	t.Helper()
	d, rec := queuetest.NewDelivery(body)
	rec.RetryErr = retryErr
	c := NewAMQPConsumer(&queuetest.Client{Deliveries: []queue.Delivery{d}}, svc, observability.NewNoopRecorder())
	if err := c.Run(context.Background()); !errors.Is(err, queue.ErrDeliveriesClosed) {
		t.Fatalf("Run: %v, want ErrDeliveriesClosed", err)
	}
	return rec
}

func validJob(t *testing.T) (shared.FetchJob, []byte) {
	t.Helper()
	job := shared.FetchJob{PipelineVersion: "1.0.0", SourceID: uuid.New(), URL: "http://src/listings/7", ExternalID: "7"}
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
	if len(svc.calls) != 1 || svc.calls[0] != (fetchCall{job.SourceID, job.URL, job.ExternalID}) {
		t.Errorf("calls=%+v — поля job не дошли как есть", svc.calls)
	}
}

func TestConsumer_MalformedBody_NacksWithoutRequeue(t *testing.T) {
	svc := &fakeSvc{}
	rec := run(t, []byte("{nope"), svc, nil)
	if len(rec.Nacks) != 1 || rec.Nacks[0] != false || len(svc.calls) != 0 {
		t.Errorf("nacks=%v calls=%d, want [false]/0", rec.Nacks, len(svc.calls))
	}
}

func TestConsumer_TransientError_BacksOff(t *testing.T) {
	_, body := validJob(t)
	rec := run(t, body, &fakeSvc{err: errors.New("get: status 429 (rate limited)")}, nil)
	if rec.Retries != 1 || len(rec.Nacks) != 0 || rec.Acks != 0 {
		t.Errorf("retries=%d nacks=%v acks=%d, want 1/[]/0", rec.Retries, rec.Nacks, rec.Acks)
	}
	if rec.LastRetryReason != "get: status 429 (rate limited)" {
		t.Errorf("LastRetryReason=%q — x-last-error (CLAUDE.md §8) должен нести текст ошибки Fetch, у неё нет pipeline_events", rec.LastRetryReason)
	}
}

func TestConsumer_PermanentError_GoesStraightToDLQ(t *testing.T) {
	_, body := validJob(t)
	perm := errors.Join(shareddomain.ErrPermanent, errors.New("status 403"))
	rec := run(t, body, &fakeSvc{err: perm}, nil)

	// DeadLetter (не Nack — тот не даёт приложить x-last-error), retry-ступени пропускаются.
	if rec.DeadLetters != 1 || rec.Retries != 0 || len(rec.Nacks) != 0 {
		t.Errorf("deadLetters=%d retries=%d nacks=%v, want 1/0/[] — 403 повтором не лечится, три круга ожидания впустую", rec.DeadLetters, rec.Retries, rec.Nacks)
	}
}

func TestConsumer_ContextCancelReturnsCtxErrNotDeliveriesClosed(t *testing.T) {
	// Штатная остановка (SIGTERM) и обрыв соединения — разные причины выхода:
	// первое не должно выглядеть как авария.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewAMQPConsumer(&queuetest.Client{}, &fakeSvc{}, observability.NewNoopRecorder())
	if err := c.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run=%v, want context.Canceled", err)
	}
}
