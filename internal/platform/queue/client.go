// Package queue — тонкая обёртка над amqp091-go, переиспользуемая
// inbound/outbound-адаптерами разных стадий (CLAUDE.md §8, "Общая
// инфраструктура"). Не domain — здесь можно и нужно импортировать
// сторонние библиотеки, чего domain-пакетам делать нельзя.
package queue

import (
	"context"
	"errors"
)

// Client — контракт, которым пользуются adapter/inbound и adapter/outbound
// каждой стадии; реальная реализация — AMQPClient (amqp_client.go), Manual
// ACK — обязательное свойство (см. CLAUDE.md §4.4).
//
// Объявление топологии (exchange/queue/binding/DLX) сознательно НЕ метод
// этого интерфейса — было им (`DeclareStage(ctx, stage string) error`) до
// того, как появилась реальная реализация: сигнатура оказалась слишком
// generic для настоящей разницы direct/topic/fanout+DLX между стадиями
// (CLAUDE.md §4.3). Топология — забота composition root при старте
// (`AMQPClient.Declare`, topology.go), не что-то, что нужно каждому
// inbound/outbound-адаптеру через общий интерфейс.
type Client interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte) error
	Consume(ctx context.Context, queue string, prefetch int) (<-chan Delivery, error)
}

// Delivery — минимальный контракт одного сообщения; Ack/Nack/Retry — обязаны
// вызываться явно (manual ack), auto-ack не используется нигде в проекте.
type Delivery struct {
	Body []byte
	Ack  func() error
	Nack func(requeue bool) error

	// Retry — ВРЕМЕННЫЙ сбой: вернуть сообщение в обработку позже (backoff
	// 1/2/4 мин через wait-очередь, счётчик попыток в заголовке), а когда
	// лимит попыток исчерпан — в DLQ. Заменяет Nack(true): мгновенный
	// requeue при недоступной зависимости — busy-loop, тот самый
	// антипаттерн из ТЕОРИЯ-УГЛУБЛЁННО.md §2.6. Сам подтверждает (ack)
	// исходное сообщение — вызывающий после Retry больше ничего не делает.
	Retry func(ctx context.Context) error
}

// ErrDeliveriesClosed — канал доставок закрылся, а ctx жив: оборвалось
// соединение с брокером. Consumer возвращает это (а не nil), иначе воркер
// завершился бы с кодом 0 — оркестратор не увидел бы падения и не
// перезапустил бы его.
var ErrDeliveriesClosed = errors.New("queue: deliveries channel closed while context still alive")

// Fail — единая реакция consumer'а на ошибку обработки: постоянная (retry не
// поможет) — сразу в DLQ (Nack без requeue), иначе — backoff через Retry.
func (d Delivery) Fail(ctx context.Context, permanent bool) {
	if permanent {
		_ = d.Nack(false)
		return
	}
	d.RetryLater(ctx)
}

// RetryLater — Retry с последним рубежом: если сам retry-механизм
// недоступен (публикация в wait-очередь не подтверждена брокером), сообщение
// возвращается через Nack(requeue=true), а не остаётся висеть
// неподтверждённым до разрыва канала. Это тот самый мгновенный requeue, но
// только при отказе самого retry-контура — не штатный путь обработки сбоев.
func (d Delivery) RetryLater(ctx context.Context) {
	if err := d.Retry(ctx); err != nil {
		_ = d.Nack(true)
	}
}
