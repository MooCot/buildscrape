package queue

import (
	"strings"
	"testing"
	"time"

	"buildscrape/internal/core"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestAttemptFromHeaders(t *testing.T) {
	cases := []struct {
		name string
		h    amqp.Table
		want int
	}{
		{"nil table — первая доставка", nil, 0},
		{"нет заголовка", amqp.Table{"other": 1}, 0},
		{"int32", amqp.Table{attemptHeader: int32(2)}, 2},
		{"int64", amqp.Table{attemptHeader: int64(3)}, 3},
		{"int", amqp.Table{attemptHeader: 1}, 1},
		{"мусорный тип — не паника, а 0", amqp.Table{attemptHeader: "two"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := attemptFromHeaders(tc.h); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPlanRetry_ExhaustsExactlyAfterMaxAttempts(t *testing.T) {
	for current := 0; current < core.MaxRetryAttempts; current++ {
		next, exhausted := planRetry(current)
		if exhausted {
			t.Errorf("current=%d: exhausted раньше времени (next=%d, лимит %d)", current, next, core.MaxRetryAttempts)
		}
		if next != current+1 {
			t.Errorf("current=%d: next=%d, want %d", current, next, current+1)
		}
	}
	// Последняя допустимая попытка уже была — следующий сбой идёт в DLQ.
	if _, exhausted := planRetry(core.MaxRetryAttempts); !exhausted {
		t.Errorf("current=%d (=MaxRetryAttempts): want exhausted — иначе poison pill крутится дальше лимита", core.MaxRetryAttempts)
	}
}

func TestRetryDelay_DoublesEachAttempt(t *testing.T) {
	want := []time.Duration{
		core.RetryBaseDelay,     // 1 мин
		2 * core.RetryBaseDelay, // 2 мин
		4 * core.RetryBaseDelay, // 4 мин
	}
	for i, w := range want {
		if got := retryDelay(i + 1); got != w {
			t.Errorf("retryDelay(%d)=%v, want %v", i+1, got, w)
		}
	}
	if got := retryDelay(0); got != core.RetryBaseDelay {
		t.Errorf("retryDelay(0)=%v — некорректная попытка должна давать базовую задержку, не 0/отрицательную", got)
	}
}

func TestRetryQueueArgs_DeadLettersBackToMainQueueViaDefaultExchange(t *testing.T) {
	args := retryQueueArgs("parse.queue", 2)

	if got := args["x-message-ttl"]; got != int32((2 * core.RetryBaseDelay).Milliseconds()) {
		t.Errorf("x-message-ttl=%v, want %d мс", got, (2 * core.RetryBaseDelay).Milliseconds())
	}
	// "" — дефолтный exchange: возврат ПРЯМО в основную очередь по имени,
	// минуя основной exchange (тот бывает topic, исходный routing key потерян).
	if got := args["x-dead-letter-exchange"]; got != "" {
		t.Errorf("x-dead-letter-exchange=%q, want \"\" (default exchange)", got)
	}
	if got := args["x-dead-letter-routing-key"]; got != "parse.queue" {
		t.Errorf("x-dead-letter-routing-key=%v, want parse.queue", got)
	}
}

func TestRetryQueueName(t *testing.T) {
	if got := RetryQueueName("ai.queue", 3); got != "ai.queue.retry.3" {
		t.Errorf("got %q", got)
	}
}

func TestDLQName(t *testing.T) {
	if got := DLQName("ai.queue"); got != "ai.dlq" {
		t.Errorf("got %q", got)
	}
}

// AllQueueNames — F11/П-metrics перебирает ровно этот список, не строит
// свой: 5 стадий × (1 основная + 3 retry + 1 DLQ) = 25 (CLAUDE.md §10.1).
func TestAllQueueNames(t *testing.T) {
	names := AllQueueNames(Topology)
	if len(names) != len(Topology)*5 {
		t.Fatalf("got %d names, want %d (5 per stage)", len(names), len(Topology)*5)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range []string{
		"ai.queue", "ai.queue.retry.1", "ai.queue.retry.2", "ai.queue.retry.3", "ai.dlq",
	} {
		if !seen[want] {
			t.Errorf("missing %q in AllQueueNames result: %v", want, names)
		}
	}
}

// Инварианты реестра Topology, нарушение которых — не ошибка компиляции,
// а падение на старте или молчаливая потеря сообщений.
func TestTopology_Invariants(t *testing.T) {
	for _, s := range Topology {
		if !strings.HasSuffix(s.Queue, ".queue") {
			// declareStage строит имя DLQ срезом суффикса ".queue" — иначе паника.
			t.Errorf("%s: имя очереди должно оканчиваться на .queue", s.Queue)
		}
		if s.Retry && !s.DLX {
			// Исчерпавшее попытки сообщение уходит Nack(false) → DLX; без DLX
			// оно было бы просто выброшено.
			t.Errorf("%s: Retry=true требует DLX=true — иначе после MaxRetryAttempts сообщение теряется", s.Queue)
		}
	}
}

// Каждая pipeline-очередь имеет consumer'а, значит, каждой нужен backoff:
// иначе временный сбой (429 источника, недоступная БД) уходил бы либо в
// мгновенный requeue, либо сразу в DLQ.
func TestTopology_EveryPipelineQueueHasRetryTiers(t *testing.T) {
	want := map[string]bool{"fetch.queue": false, "parse.queue": false, "media.queue": false, "geocode.queue": false, "ai.queue": false}
	for _, s := range Topology {
		if _, ok := want[s.Queue]; ok {
			want[s.Queue] = s.Retry
		}
	}
	for q, has := range want {
		if !has {
			t.Errorf("%s: Retry=false, а у очереди есть consumer", q)
		}
	}
}
