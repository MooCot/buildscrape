package queue

import (
	"context"
	"errors"
	"fmt"

	"buildscrape/internal/core"

	amqp "github.com/rabbitmq/amqp091-go"
)

// AMQPClient — реальная реализация Client поверх amqp091-go. Одно
// соединение + один канал на процесс — упрощение этого инкремента; канал
// в AMQP не потокобезопасен для конкурентной публикации с разных goroutine
// без внешней синхронизации, а consumer здесь читает свой собственный канал
// сообщений, не делится каналом AMQP с publish-путём той же роли. Если
// профиль нагрузки потребует иного — отдельный канал на publisher/на
// consumer, TODO на будущий инкремент, не спроектировано здесь заранее
// вхолостую.
type AMQPClient struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

// Dial — открывает соединение + канал. Не ретраит и не скрывает ошибку —
// вызывающий (composition root, cmd/buildscrape) решает, что делать при
// недоступном брокере на старте (упасть/подождать/backoff) — не дело этого
// пакета решать за приложение политику старта.
//
// Канал переводится в confirm-режим: Publish дожидается подтверждения
// брокера. Без этого ack исходного сообщения после "успешной" публикации
// (hand-off между стадиями, Retry) мог бы состояться при молча потерянной
// публикации — сообщение исчезло бы без следа.
func Dial(url string) (*AMQPClient, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("amqp dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("amqp channel: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("amqp confirm mode: %w", err)
	}
	return &AMQPClient{conn: conn, ch: ch}, nil
}

func (c *AMQPClient) Close() error {
	chErr := c.ch.Close()
	connErr := c.conn.Close()
	if chErr != nil {
		return chErr
	}
	return connErr
}

// Declare — идемпотентно объявляет ВСЮ топологию (exchange/queue/binding +
// DLX/DLQ + retry-ступени на стадию, где нужны) одним вызовом при старте.
// Явно, а не implicit внутри каждого Publish/Consume — там уже поздно:
// очередь должна существовать РАНЬШЕ первого сообщения, и повторная
// декларация на каждый вызов Publish была бы лишним round-trip к брокеру
// на каждое сообщение.
func (c *AMQPClient) Declare(specs []StageSpec) error {
	if err := c.ch.ExchangeDeclare(PipelineEventsExchange, "fanout", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare %s: %w", PipelineEventsExchange, err)
	}
	for _, s := range specs {
		if err := c.declareStage(s); err != nil {
			return fmt.Errorf("declare stage %s: %w", s.Queue, err)
		}
	}
	return nil
}

func (c *AMQPClient) declareStage(s StageSpec) error {
	args := amqp.Table{}

	if s.DLX {
		dlx := s.Exchange + ".dlx"
		// fetch.queue -> fetch.dlq (тот же паттерн, что и CLAUDE.md §4.3/§5:
		// суффикс .queue отбрасывается, не .dlx приклеивается поверх .queue)
		dlq := s.Queue[:len(s.Queue)-len(".queue")] + ".dlq"

		if err := c.ch.ExchangeDeclare(dlx, "fanout", true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare dlx %s: %w", dlx, err)
		}
		if _, err := c.ch.QueueDeclare(dlq, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare dlq %s: %w", dlq, err)
		}
		if err := c.ch.QueueBind(dlq, "", dlx, false, nil); err != nil {
			return fmt.Errorf("bind dlq %s: %w", dlq, err)
		}
		args["x-dead-letter-exchange"] = dlx
	}

	if err := c.ch.ExchangeDeclare(s.Exchange, s.ExchangeType, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", s.Exchange, err)
	}
	if _, err := c.ch.QueueDeclare(s.Queue, true, false, false, false, args); err != nil {
		return fmt.Errorf("declare queue %s: %w", s.Queue, err)
	}
	if err := c.ch.QueueBind(s.Queue, s.RoutingKey, s.Exchange, false, nil); err != nil {
		return fmt.Errorf("bind queue %s: %w", s.Queue, err)
	}

	if s.Retry {
		// Ступени без binding'а к какому-либо exchange: публикуются через
		// дефолтный exchange по имени очереди (см. retry() ниже).
		for attempt := 1; attempt <= core.MaxRetryAttempts; attempt++ {
			name := retryQueueName(s.Queue, attempt)
			if _, err := c.ch.QueueDeclare(name, true, false, false, false, retryQueueArgs(s.Queue, attempt)); err != nil {
				return fmt.Errorf("declare retry queue %s: %w", name, err)
			}
		}
	}
	return nil
}

// Publish — persistent-сообщение (DeliveryMode: 2), переживает рестарт
// брокера (durable queue + persistent message — обе половины нужны,
// одного durable queue недостаточно без persistent message на нём).
func (c *AMQPClient) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	return c.publish(ctx, exchange, routingKey, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         body,
	})
}

// publish — публикация с ожиданием подтверждения брокера (confirm-режим
// включён в Dial). nack от брокера — ошибка, не тихий успех.
func (c *AMQPClient) publish(ctx context.Context, exchange, routingKey string, msg amqp.Publishing) error {
	dc, err := c.ch.PublishWithDeferredConfirmWithContext(ctx, exchange, routingKey, false, false, msg)
	if err != nil {
		return fmt.Errorf("publish %s/%s: %w", exchange, routingKey, err)
	}
	if dc == nil {
		return nil // канал не в confirm-режиме — в Dial это невозможно, но не паникуем
	}
	acked, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("publish %s/%s: wait confirm: %w", exchange, routingKey, err)
	}
	if !acked {
		return errors.New("publish " + exchange + "/" + routingKey + ": nacked by broker")
	}
	return nil
}

// retry — см. Delivery.Retry. Порядок "публикация в wait-очередь → ack
// оригинала": при падении между шагами сообщение обработается дважды
// (at-least-once), что допустимо — consumer'ы идемпотентны (ON CONFLICT
// upsert, ТЕОРИЯ-УГЛУБЛЁННО.md §6.2); обратный порядок терял бы сообщение.
func (c *AMQPClient) retry(ctx context.Context, queue string, d amqp.Delivery) error {
	next, exhausted := planRetry(attemptFromHeaders(d.Headers))
	if exhausted {
		// Nack без requeue → x-dead-letter-exchange основной очереди → DLQ.
		return d.Nack(false, false)
	}
	err := c.publish(ctx, "", retryQueueName(queue, next), amqp.Publishing{
		ContentType:  d.ContentType,
		DeliveryMode: amqp.Persistent,
		Headers:      amqp.Table{attemptHeader: int32(next)},
		Body:         d.Body,
	})
	if err != nil {
		return err
	}
	return d.Ack(false)
}

// Consume — autoAck=false (manual ack, ТЕОРИЯ-УГЛУБЛЁННО.md §2.2, обязательно
// для этого проекта); QoS/prefetch — per-consumer, не глобальный на канал
// (global=false в Qos), чтобы разные стадии/воркеры на РАЗНЫХ каналах не
// делили один и тот же prefetch-лимит нечаянно.
func (c *AMQPClient) Consume(ctx context.Context, queue string, prefetch int) (<-chan Delivery, error) {
	if err := c.ch.Qos(prefetch, 0, false); err != nil {
		return nil, fmt.Errorf("qos: %w", err)
	}
	raw, err := c.ch.ConsumeWithContext(ctx, queue, "", false, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("consume %s: %w", queue, err)
	}

	out := make(chan Delivery)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-raw:
				if !ok {
					return // канал amqp091-go закрылся (разрыв соединения) — consumer выше видит закрытие out и завершается
				}
				delivery := d // отдельная переменная на итерацию — захват в замыканиях Ack/Nack ниже
				select {
				case out <- Delivery{
					Body:  delivery.Body,
					Ack:   func() error { return delivery.Ack(false) }, // multiple=false — только это сообщение
					Nack:  func(requeue bool) error { return delivery.Nack(false, requeue) },
					Retry: func(ctx context.Context) error { return c.retry(ctx, queue, delivery) },
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}
