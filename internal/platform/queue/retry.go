package queue

import (
	"fmt"
	"time"

	"buildscrape/internal/core"

	amqp "github.com/rabbitmq/amqp091-go"
)

// attemptHeader — счётчик попыток в заголовке сообщения (CLAUDE.md §4.4:
// "со счётчиком попыток в заголовке сообщения до перехода в DLQ"). Первая
// доставка заголовка не несёт — attempt=0.
const attemptHeader = "x-attempt"

// attemptFromHeaders — числа в amqp.Table приходят разными целочисленными
// типами в зависимости от того, чем их закодировал отправитель, поэтому
// type switch, а не одно приведение.
func attemptFromHeaders(h amqp.Table) int {
	switch v := h[attemptHeader].(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	default:
		return 0
	}
}

// planRetry — чистое решение "что делать с сообщением после сбоя":
// next — номер СЛЕДУЮЩЕЙ попытки; exhausted — лимит core.MaxRetryAttempts
// исчерпан, сообщение идёт в DLQ, а не в очередной круг ожидания (poison
// pill не крутится вечно, ТЕОРИЯ-УГЛУБЛЁННО.md §6.4).
func planRetry(current int) (next int, exhausted bool) {
	next = current + 1
	return next, next > core.MaxRetryAttempts
}

// retryDelay — экспоненциальный backoff: попытка 1 → base, 2 → 2×base, 3 →
// 4×base (1/2/4 мин при RetryBaseDelay=1мин, internal/core).
func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return core.RetryBaseDelay << (attempt - 1)
}

// RetryQueueName — экспортирована (CLAUDE.md §10.3): F11 (опрос длины
// очередей, MODEL-buildscrape.md) перебирает retry-ступени каждой стадии
// и не должен отдельной копией повторять эту строковую формулу.
func RetryQueueName(mainQueue string, attempt int) string {
	return fmt.Sprintf("%s.retry.%d", mainQueue, attempt)
}

// DLQName — тот же случай: вынесено из inline-логики внутри declareStage,
// чтобы имя DLQ не вычислялось дважды двумя разными строковыми операциями
// в двух местах пакета.
func DLQName(mainQueue string) string {
	return mainQueue[:len(mainQueue)-len(".queue")] + ".dlq"
}

// retryQueueArgs — аргументы wait-очереди одной ступени. Сообщение живёт в
// ней ровно retryDelay(attempt) (per-queue TTL — у всех сообщений ступени
// одинаковый, поэтому порядок истечения совпадает с FIFO) и по истечении
// dead-letter'ится в ДЕФОЛТНЫЙ exchange ("") с routing key = имя основной
// очереди — то есть возвращается прямо в неё, минуя основной exchange
// (тот бывает topic с паттерном, а исходный routing key при повторной
// публикации уже потерян).
func retryQueueArgs(mainQueue string, attempt int) amqp.Table {
	return amqp.Table{
		"x-message-ttl":             int32(retryDelay(attempt).Milliseconds()),
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": mainQueue,
	}
}
