package queue

import "context"

// NoopClient — заглушка Client для composition root, когда реальный
// брокер не поднят (демо/юнит-уровень) — см. db.NoopPool doc-comment, тот
// же принцип. AMQPClient (amqp_client.go) — реальная реализация поверх
// amqp091-go, требует поднятый RabbitMQ (docker compose up, не
// автозапускается кодом).
type NoopClient struct{}

func NewNoopClient() *NoopClient { return &NoopClient{} }

func (c *NoopClient) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	return nil
}

func (c *NoopClient) Consume(ctx context.Context, queue string, prefetch int) (<-chan Delivery, error) {
	ch := make(chan Delivery) // никогда не закрывается сам — держит consumer живым до ctx.Done()
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}
