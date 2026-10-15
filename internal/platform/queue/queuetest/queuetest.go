// Package queuetest — fake queue.Client для юнит-тестов inbound-consumer'ов:
// подкладывает заранее заданные сообщения и записывает, что consumer с
// каждым сделал (Ack / Nack(requeue) / Retry). Не mock в смысле "проверить
// вызов метода с аргументами" — реальный, пусть упрощённый, клиент в
// памяти, ловящий итоговое состояние (ТЕОРИЯ-УГЛУБЛЁННО.md §1.8).
package queuetest

import (
	"context"

	"buildscrape/internal/platform/queue"
)

// Recorder — что consumer сделал с ОДНИМ сообщением.
type Recorder struct {
	Acks    int
	Nacks   []bool // requeue-флаг каждого вызова Nack, по порядку
	Retries int

	// RetryErr — что вернёт Retry (имитация недоступного retry-механизма:
	// например, публикация в wait-очередь не подтверждена брокером).
	RetryErr error
}

// NewDelivery — сообщение с body и Recorder, в который пишутся действия.
func NewDelivery(body []byte) (queue.Delivery, *Recorder) {
	r := &Recorder{}
	return queue.Delivery{
		Body: body,
		Ack:  func() error { r.Acks++; return nil },
		Nack: func(requeue bool) error { r.Nacks = append(r.Nacks, requeue); return nil },
		Retry: func(ctx context.Context) error {
			r.Retries++
			return r.RetryErr
		},
	}, r
}

// Client — реализует queue.Client: Consume отдаёт подготовленные сообщения
// и закрывает канал, поэтому consumer.Run возвращается сам, без отмены ctx.
type Client struct {
	Deliveries []queue.Delivery
}

func (c *Client) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	return nil
}

func (c *Client) Consume(ctx context.Context, queueName string, prefetch int) (<-chan queue.Delivery, error) {
	ch := make(chan queue.Delivery, len(c.Deliveries))
	for _, d := range c.Deliveries {
		ch <- d
	}
	close(ch)
	return ch, nil
}
