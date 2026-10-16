//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"
)

// TestRetryLadder_DeadLetterReturnsMessageToMainQueue — E2E-3 (CLAUDE.md:152
// "backoff через retry-ступени... со счётчиком попыток"; retry.go/
// amqp_client.go — x-dead-letter-exchange: "" трюк: wait-очередь не
// привязана ни к какому exchange, по истечении TTL дефолтный exchange
// доставляет сообщение обратно в основную очередь по routing key = её
// имени). НЕ в MODEL-buildscrape.md — осознанно: retry/DLQ — инфраструктура
// RabbitMQ, не Entity/Гейт домена (CLAUDE.md §8: internal/platform/queue —
// "не hexagon... порты в стене"), в отличие от SourcePollState (§1.1bis),
// у которой была скрытая identity. Fake queue.Client в юнит-тестах не
// воспроизводит ни TTL wait-очереди, ни dead-letter через дефолтный
// exchange реального брокера — только живой RabbitMQ может подтвердить
// этот механизм.
//
// Проверяет только первую ступень (1 мин TTL, core.RetryBaseDelay) — она
// уже доказывает механизм; эскалация до DLQ через все 3 ступени (1+2+4=7
// мин) добавила бы 6 минут без нового знания. Delivery не отдаёт
// заголовки (x-attempt) наружу — проверяется факт возврата, не инкремент
// счётчика напрямую.
func TestRetryLadder_DeadLetterReturnsMessageToMainQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("real TTL wait (~65s) — run explicitly, not with -short")
	}

	client := setupRabbitMQ(t)
	ctx := context.Background()

	const body = `{"e2e":"retry-ladder"}`
	if err := client.Publish(ctx, "fetch.exchange", "", []byte(body)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deliveries, err := client.Consume(ctx, "fetch.queue", 1)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}

	first, ok := <-deliveries
	if !ok {
		t.Fatal("deliveries channel closed before first delivery")
	}
	if string(first.Body) != body {
		t.Fatalf("unexpected body: %s", first.Body)
	}

	// Временный сбой — тот же путь, что shareddomain.IsPermanent(err)=false
	// в реальном consumer'е (queue.Delivery.Fail(ctx, false) делегирует
	// сюда же).
	if err := first.Retry(ctx); err != nil {
		t.Fatalf("retry: %v", err)
	}

	if n, err := client.QueueLength(ctx, "fetch.queue.retry.1"); err != nil {
		t.Fatalf("queue length retry.1: %v", err)
	} else if n != 1 {
		t.Fatalf("expected 1 message in fetch.queue.retry.1 right after Retry, got %d", n)
	}

	t.Log("waiting out the real 1-minute retry.1 TTL (core.RetryBaseDelay) — this is the slow part")
	select {
	case second, ok := <-deliveries:
		if !ok {
			t.Fatal("deliveries channel closed while waiting for the dead-lettered redelivery")
		}
		if string(second.Body) != body {
			t.Fatalf("redelivered body mismatch: %s", second.Body)
		}
		if err := second.Ack(); err != nil {
			t.Fatalf("ack redelivered message: %v", err)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("message never came back to fetch.queue within 90s of a 1-minute TTL — dead-letter trick did not fire")
	}

	if n, err := client.QueueLength(ctx, "fetch.queue.retry.1"); err != nil {
		t.Fatalf("queue length retry.1 after dead-letter: %v", err)
	} else if n != 0 {
		t.Fatalf("expected fetch.queue.retry.1 empty after dead-letter, got %d", n)
	}
}
