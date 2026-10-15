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
	d.Fail(context.Background(), true)

	// Nack без requeue → DLX → DLQ; retry-ступени пропускаются.
	if len(rec.Nacks) != 1 || rec.Nacks[0] != false || rec.Retries != 0 || rec.Acks != 0 {
		t.Errorf("nacks=%v retries=%d acks=%d, want [false]/0/0", rec.Nacks, rec.Retries, rec.Acks)
	}
}

func TestDeliveryFail_TransientGoesToBackoff(t *testing.T) {
	d, rec := queuetest.NewDelivery(nil)
	d.Fail(context.Background(), false)

	if rec.Retries != 1 || len(rec.Nacks) != 0 || rec.Acks != 0 {
		t.Errorf("retries=%d nacks=%v acks=%d, want 1/[]/0", rec.Retries, rec.Nacks, rec.Acks)
	}
}

func TestDeliveryFail_TransientWithBrokenRetryFallsBackToRequeue(t *testing.T) {
	d, rec := queuetest.NewDelivery(nil)
	rec.RetryErr = errors.New("retry publish nacked")
	d.Fail(context.Background(), false)

	if len(rec.Nacks) != 1 || rec.Nacks[0] != true {
		t.Errorf("nacks=%v, want [true]", rec.Nacks)
	}
}

var _ = queue.ErrDeliveriesClosed // сентинел — часть публичного контракта пакета
