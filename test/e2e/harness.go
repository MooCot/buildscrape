//go:build e2e

// Package e2e — тесты на testcontainers (CLAUDE.md "Не сделано" →
// MODEL-buildscrape.md §10 TDD-порядок, подмножество: только то, что fake-
// порты не воспроизводят — ON CONFLICT под реальным Postgres, реальная
// доставка AMQP). Build tag `e2e` держит их вне `go test ./...`/`make
// test-unit` — отдельная, медленная команда `make test-e2e`, требует
// Docker (testcontainers поднимает его сам, не docker-compose).
package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"buildscrape/internal/platform/db"
	"buildscrape/internal/platform/queue"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupPostgres — реальный Postgres 16.4 (та же версия, что
// deployments/docker-compose.yml) в контейнере + применённые миграции
// (`migrations/*.sql`, тот же порядок по имени, что
// docker-entrypoint-initdb.d использует в compose). Контейнер
// останавливается автоматически в конце теста (t.Cleanup).
func setupPostgres(t *testing.T) db.Pool {
	t.Helper()
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, "postgres:16.4",
		postgres.WithDatabase("buildscrape"),
		postgres.WithUsername("buildscrape"),
		postgres.WithPassword("buildscrape"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := db.NewPgxPool(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)

	applyMigrations(t, ctx, pool)
	return pool
}

// setupRabbitMQ — тот же образ/версия, что deployments/docker-compose.yml
// (`rabbitmq:3.13-management`), с ПОЛНОЙ топологией (`queue.Topology`,
// amqp_client.go `Declare`) — то же самое, что composition root делает
// при старте каждой роли, не урезанный тестовый стенд.
func setupRabbitMQ(t *testing.T) (queue.Client, *rabbitmq.RabbitMQContainer) {
	t.Helper()
	ctx := context.Background()

	// Дедлайн 3 мин, не дефолтные 60с — на этой машине RabbitMQ грузится
	// очень нестабильно (наблюдались живые прогоны от ~27с до
	// "Time to start RabbitMQ: 122062 ms" в логе самого брокера, то есть
	// 2-минутный дедлайн, поставленный раньше, тоже не всегда хватает —
	// 3 минуты как запас сверху худшего наблюдённого случая, не точное
	// значение). ДВА независимых таймаута нужно поднять, не один —
	// поднятие только одного из них всё равно даёт эффективный потолок
	// меньшей из двух величин, именно так тест и падал:
	//  1. LogStrategy.timeout (wait/log.go) — если не задан явно через
	//     .WithStartupTimeout на САМОЙ стратегии (а не снаружи), внутри
	//     WaitUntilReady используется defaultStartupTimeout()=60с через
	//     собственный derived context.WithTimeout, независимый от внешнего
	//     дедлайна MultiStrategy.
	//  2. MultiStrategy.deadline (wait/all.go, ForAll(...).WithDeadline) —
	//     внешний потолок поверх всех стратегий сразу; WithWaitStrategy
	//     (не AndDeadline) хардкодит его в 60с (options.go:314 той же
	//     библиотеки), поэтому именно AndDeadline.
	// WithAdminUsername/Password НЕ используются намеренно: модуль
	// одновременно хардкодит RABBITMQ_DEFAULT_USER/PASS=guest в Env (до
	// применения опций, rabbitmq.go Run()) И пишет default_user/default_pass
	// в конфиг-файл из этих опций — на практике выигрывает guest/guest
	// (конфликт самого модуля, не этого теста); guest/guest достаточно,
	// тест про механику retry/DLQ, не про credentials.
	ctr, err := rabbitmq.Run(ctx, "rabbitmq:3.13-management",
		testcontainers.WithWaitStrategyAndDeadline(
			3*time.Minute,
			wait.ForLog("Server startup complete").WithStartupTimeout(3*time.Minute),
		),
	)
	if err != nil {
		t.Fatalf("start rabbitmq container: %v", err)
	}
	t.Cleanup(func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("terminate rabbitmq container: %v", err)
		}
	})

	url, err := ctr.AmqpURL(ctx)
	if err != nil {
		t.Fatalf("amqp url: %v", err)
	}

	client, err := queue.Dial(url)
	if err != nil {
		t.Fatalf("dial rabbitmq: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	if err := client.Declare(queue.Topology); err != nil {
		t.Fatalf("declare topology: %v", err)
	}
	return client, ctr
}

// applyMigrations — те же файлы, что docker-entrypoint-initdb.d монтирует
// в проде (deployments/docker-compose.yml), по алфавиту, одним Exec на
// файл. Не DDL-инструмент на будущее — ровно то, что нужно тестовому
// контейнеру с пустым томом, см. migrations/README-подобный комментарий в
// самом docker-compose.yml.
func applyMigrations(t *testing.T, ctx context.Context, pool db.Pool) {
	t.Helper()
	files, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob migrations: %v (files=%v)", err, files)
	}
	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read migration %s: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %s: %v", f, err)
		}
	}
}
