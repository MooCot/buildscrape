// Package inbound (fetch) — два входа в Fetch: таймер планировщика (F1a,
// PollTrigger) и consumer fetch.queue (F1b, AMQPConsumer).
package inbound

import (
	"context"
	"log/slog"
	"time"
)

// Scheduler — то, что нужно триггеру от домена (интерфейс определён на
// стороне потребителя: триггеру не нужен весь fetch/domain.Service, а тест
// подставляет крошечный fake).
type Scheduler interface {
	ScheduleDue(ctx context.Context) error
}

// PollTrigger — таймер планировщика: раз в interval вызывает ScheduleDue.
// Первый тик — сразу при старте, а не через interval: иначе только что
// поднятый воркер полминуты ничего не делал бы при уже созревших источниках.
//
// Безопасно запускать в КАЖДОМ Fetch-воркере: ClaimDue атомарен, источник
// достанется ровно одному из них (см. fetch/domain.SourceScheduler).
type PollTrigger struct {
	svc      Scheduler
	interval time.Duration
}

func NewPollTrigger(svc Scheduler, interval time.Duration) *PollTrigger {
	return &PollTrigger{svc: svc, interval: interval}
}

func (t *PollTrigger) Run(ctx context.Context) error {
	t.tick(ctx)

	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err() // graceful shutdown — см. ТЕОРИЯ-УГЛУБЛЁННО.md §8.3
		case <-ticker.C:
			t.tick(ctx)
		}
	}
}

// tick — ошибка планирования не должна убивать воркер (сломанный источник
// не повод останавливать опрос остальных), но и глотаться молча она не
// вправе: логируется с контекстом. Счётчик метрики — когда появится
// observability.Recorder.
func (t *PollTrigger) tick(ctx context.Context) {
	if err := t.svc.ScheduleDue(ctx); err != nil && ctx.Err() == nil {
		slog.Error("fetch: schedule due sources failed", "err", err)
	}
}
