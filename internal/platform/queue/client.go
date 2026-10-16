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

	// QueueLength — текущая глубина очереди, без побочных эффектов
	// (MODEL-buildscrape.md F11/§1.5bis — П-metrics, CLAUDE.md §10.3).
	// Не публикует, не консьюмит, не меняет очередь.
	QueueLength(ctx context.Context, queue string) (int, error)
}

// Delivery — минимальный контракт одного сообщения; Ack/Nack/Retry/
// DeadLetter — обязаны вызываться явно (manual ack), auto-ack не
// используется нигде в проекте.
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
	// reason — текст ошибки, попадает в заголовок x-last-error (retry.go)
	// следующей копии сообщения.
	Retry func(ctx context.Context, reason string) error

	// DeadLetter — постоянный сбой: ручная публикация копии сообщения (с
	// заголовком x-last-error) в DLX стадии + Ack оригинала. НЕ Nack(false)
	// — тот отдаёт dead-letter нативному механизму RabbitMQ, который копирует
	// заголовки как есть, но не даёт приложить новый (CLAUDE.md §8,
	// internal/platform/queue — инфра-механизм, симметричный
	// pipeline_events для стадий, у которых его нет, в первую очередь Fetch,
	// MODEL §1.5bis). Сам подтверждает (ack) исходное сообщение.
	DeadLetter func(ctx context.Context, reason string) error
}

// ErrDeliveriesClosed — канал доставок закрылся, а ctx жив: оборвалось
// соединение с брокером. Consumer возвращает это (а не nil), иначе воркер
// завершился бы с кодом 0 — оркестратор не увидел бы падения и не
// перезапустил бы его.
var ErrDeliveriesClosed = errors.New("queue: deliveries channel closed while context still alive")

// Fail — единая реакция consumer'а на ошибку обработки: постоянная (retry не
// поможет) — сразу в DLQ (DeadLetter, не Nack — несёт reason), иначе —
// backoff через Retry. reason — err.Error() вызывающего, попадает в
// x-last-error на итоговом сообщении в любом случае.
func (d Delivery) Fail(ctx context.Context, permanent bool, reason string) {
	if permanent {
		if err := d.DeadLetter(ctx, reason); err != nil {
			// Запасной путь — нативный DLX всё равно сработает через
			// Nack(false), просто без x-last-error на этот раз (тот же
			// принцип "деградирует отдельно", что у best-effort метрик,
			// CLAUDE.md §10.5) — не оставляем сообщение неподтверждённым.
			_ = d.Nack(false)
		}
		return
	}
	d.RetryLater(ctx, reason)
}

// RetryLater — Retry с последним рубежом: если сам retry-механизм
// недоступен (публикация в wait-очередь не подтверждена брокером), сообщение
// возвращается через Nack(requeue=true), а не остаётся висеть
// неподтверждённым до разрыва канала. Это тот самый мгновенный requeue, но
// только при отказе самого retry-контура — не штатный путь обработки сбоев.
func (d Delivery) RetryLater(ctx context.Context, reason string) {
	if err := d.Retry(ctx, reason); err != nil {
		_ = d.Nack(true)
	}
}
