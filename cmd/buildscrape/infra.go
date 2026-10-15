package main

import (
	"context"

	"buildscrape/internal/platform/db"
	"buildscrape/internal/platform/queue"
)

// newQueueClient — dsn/url пуст ⟹ Noop (демо/локально без брокера/БД,
// см. queue.NoopClient/db.NoopPool doc-comment); иначе — реальный клиент.
// Общий для serve.go и worker.go — composition root каждой роли решает
// САМ, звать ли эти хелперы (serve сейчас БД трогает, очередь — нет; F6/
// F7/F8/F9 не консьюмят/паблишат в очередь вообще, см. storage/domain).
func newQueueClient(url string) (queue.Client, error) {
	if url == "" {
		return queue.NewNoopClient(), nil
	}
	c, err := queue.Dial(url)
	if err != nil {
		return nil, err
	}
	// Топология объявляется ОДИН раз здесь (Declare, см. queue.AMQPClient
	// doc-comment) — до первого Publish/Consume, не implicit внутри них.
	if err := c.Declare(queue.Topology); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func newPool(ctx context.Context, dsn string) (db.Pool, error) {
	if dsn == "" {
		return db.NewNoopPool(), nil
	}
	return db.NewPgxPool(ctx, dsn)
}
