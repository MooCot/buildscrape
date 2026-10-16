package queue_test

import (
	"context"
	"errors"
	"testing"

	"buildscrape/internal/platform/queue"
	"buildscrape/internal/platform/queue/queuetest"
)

func TestDeliveryFail_PermanentGoesStraightToDLQ(t *testing.T) {
	d, rec := queuetest.NewDelivery(nil)
	d.Fail(context.Background(), true, "status 403")

	// DeadLetter (не Nack — тот не даёт приложить x-last-error), retry-ступени
	// пропускаются.
	if rec.DeadLetters != 1 || rec.LastDeadLetterReason != "status 403" || rec.Retries != 0 || len(rec.Nacks) != 0 || rec.Acks != 0 {
		t.Errorf("deadLetters=%d reason=%q retries=%d nacks=%v acks=%d, want 1/\"status 403\"/0/[]/0",
			rec.DeadLetters, rec.LastDeadLetterReason, rec.Retries, rec.Nacks, rec.Acks)
	}
}

// Если сама публикация в DLX не подтверждена брокером — сообщение не
// остаётся неподтверждённым, запасной путь Nack(false) (без reason —
// обзорная ветка деградирует отдельно, CLAUDE.md §10.5).
func TestDeliveryFail_PermanentWithBrokenDeadLetterFallsBackToNack(t *testing.T) {
	d, rec := queuetest.NewDelivery(nil)
	rec.DeadLetterErr = errors.New("dlx publish nacked")
	d.Fail(context.Background(), true, "status 403")

	if len(rec.Nacks) != 1 || rec.Nacks[0] != false {
		t.Errorf("nacks=%v, want [false]", rec.Nacks)
	}
}

func TestDeliveryFail_TransientGoesToBackoff(t *testing.T) {
	d, rec := queuetest.NewDelivery(nil)
	d.Fail(context.Background(), false, "db down")

	if rec.Retries != 1 || rec.LastRetryReason != "db down" || len(rec.Nacks) != 0 || rec.Acks != 0 {
		t.Errorf("retries=%d reason=%q nacks=%v acks=%d, want 1/\"db down\"/[]/0", rec.Retries, rec.LastRetryReason, rec.Nacks, rec.Acks)
	}
}

func TestDeliveryFail_TransientWithBrokenRetryFallsBackToRequeue(t *testing.T) {
	d, rec := queuetest.NewDelivery(nil)
	rec.RetryErr = errors.New("retry publish nacked")
	d.Fail(context.Background(), false, "db down")

	if len(rec.Nacks) != 1 || rec.Nacks[0] != true {
		t.Errorf("nacks=%v, want [true]", rec.Nacks)
	}
}

var _ = queue.ErrDeliveriesClosed // сентинел — часть публичного контракта пакета
