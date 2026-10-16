package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	aiadapterinbound "buildscrape/internal/ai/adapter/inbound"
	aiadapteroutbound "buildscrape/internal/ai/adapter/outbound"
	aidomain "buildscrape/internal/ai/domain"

	"buildscrape/internal/cleanup"

	fetchadapterinbound "buildscrape/internal/fetch/adapter/inbound"
	fetchadapteroutbound "buildscrape/internal/fetch/adapter/outbound"
	fetchdomain "buildscrape/internal/fetch/domain"

	geocodeadapterinbound "buildscrape/internal/geocode/adapter/inbound"
	geocodeadapteroutbound "buildscrape/internal/geocode/adapter/outbound"
	geocodedomain "buildscrape/internal/geocode/domain"

	mediaadapterinbound "buildscrape/internal/media/adapter/inbound"
	mediaadapteroutbound "buildscrape/internal/media/adapter/outbound"
	mediadomain "buildscrape/internal/media/domain"

	parseadapterinbound "buildscrape/internal/parse/adapter/inbound"
	parseadapteroutbound "buildscrape/internal/parse/adapter/outbound"
	parsedomain "buildscrape/internal/parse/domain"

	storageadapteroutbound "buildscrape/internal/storage/adapter/outbound"

	sharedadapteroutbound "buildscrape/internal/shared/adapter/outbound"

	"buildscrape/internal/core"
	"buildscrape/internal/platform/config"
	"buildscrape/internal/platform/queue"

	"golang.org/x/sync/errgroup"
)

// runWorker — composition root ролей "worker <stage>". Один и тот же
// gate/sourceRepo (storage/adapter/outbound) переиспользуется всеми
// стадиями, консультирующими ItemGateChecker — единый порт, не отдельная
// копия на воркер (CLAUDE.md §8, "internal/shared/domain"). newPool/
// newQueueClient — infra.go, общие с serve.go.
//
// Возвращает error (не os.Exit внутри) — main.go/cobra решают код возврата
// процесса в одном месте (см. main.go doc-comment на SilenceUsage — ошибка
// выполнения здесь НЕ ошибка вызова команды, cobra не должна на неё
// показывать usage).
func runWorker(stage string) error {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := newPool(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer pool.Close()

	// cleanup — housekeeping на своём таймере, не AMQP-консюмер (MODEL-
	// buildscrape.md §9, SR-cleanup: "не переиспользует internal/platform/
	// queue"); единственная стадия без подключения к очереди вообще.
	var q queue.Client
	qMode := "n/a (cleanup)"
	if stage != "cleanup" {
		q, err = newQueueClient(cfg.RabbitMQURL)
		if err != nil {
			return fmt.Errorf("queue connect: %w", err)
		}
		if closer, ok := q.(interface{ Close() error }); ok {
			defer closer.Close()
		}
		qMode = "noop"
		if cfg.RabbitMQURL != "" {
			qMode = "real AMQP"
		}
	}

	sourceRepo := storageadapteroutbound.NewPostgresSourceRepository(pool)
	gate := storageadapteroutbound.NewPostgresItemGateChecker(pool, sourceRepo)
	// Один EventRecorder на все 4 item-стадии (Parse/Media/Geocode/AI) — не
	// по экземпляру на стадию: MODEL-buildscrape.md §1.5 правка, см. его
	// doc-comment. Fetch/Storage/Cleanup не участвуют — у них на момент
	// действия либо ещё нет Item (Fetch, FK pipeline_events.item_id →
	// items.id не позволит), либо действие над Source, а не над Item
	// (Storage F7/F8/F9).
	var events *sharedadapteroutbound.EventRecorder
	if stage != "cleanup" {
		events = sharedadapteroutbound.NewEventRecorder(pool, q)
	}

	dbMode := "noop"
	if cfg.PostgresDSN != "" {
		dbMode = "real Postgres"
	}
	fmt.Printf("worker %s: starting (db=%s, queue=%s)\n", stage, dbMode, qMode)

	switch stage { // err уже объявлен выше (newPool/newQueueClient) — переиспользуется, не redeclare
	case "fetch":
		// Один тип воркера, две задачи параллельно: планировщик (F1a, таймер) и
		// consumer fetch.queue (F1b). Безопасно в каждом инстансе при --scale:
		// ClaimDue атомарен, источник достанется ровно одному планировщику, а
		// consumer'ы конкурируют за задания честно (competing consumers).
		fetchPub := fetchadapteroutbound.NewAMQPPublisher(q) // и JobPublisher (F1a→F1b), и NextStagePublisher (F1→F2)
		svc := fetchdomain.NewService(fetchdomain.Deps{
			Proxies:   fetchadapteroutbound.NewMockProxyProvider(3),
			Pages:     fetchadapteroutbound.NewHTTPPageFetcher(15 * time.Second),
			Index:     fetchadapteroutbound.HTMLIndexExtractor{},
			Scheduler: fetchadapteroutbound.NewPostgresSourceScheduler(pool),
			Jobs:      fetchPub,
			Writer:    fetchadapteroutbound.NewPostgresRawItemWriter(pool),
			Next:      fetchPub,
			Sources:   sourceRepo,
		})

		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error { return fetchadapterinbound.NewPollTrigger(svc, 30*time.Second).Run(gctx) })
		g.Go(func() error { return fetchadapterinbound.NewAMQPConsumer(q, svc).Run(gctx) })
		// Первая вернувшаяся ошибка отменяет gctx и останавливает вторую
		// горутину — иначе при обрыве соединения consumer'а планировщик
		// продолжал бы плодить задания в никуда.
		err = g.Wait()

	case "parse":
		parsePub := parseadapteroutbound.NewAMQPPublisher(q) // NextStagePublisher (хендофф задания)
		svc := parsedomain.NewService(parseadapteroutbound.HTMLExtractor{}, parseadapteroutbound.NewPostgresItemWriter(pool), parsePub, events)
		err = parseadapterinbound.NewAMQPConsumer(q, svc).Run(ctx)

	case "media":
		svc := mediadomain.NewService(
			gate,
			mediaadapteroutbound.NewImageMagickProcessor(),
			mediaadapteroutbound.NewMinIOStore(""),
			mediaadapteroutbound.NewPostgresMediaWriter(pool),
			events,
		)
		err = mediaadapterinbound.NewAMQPConsumer(q, svc).Run(ctx)

	case "geocode":
		svc := geocodedomain.NewService(
			gate,
			geocodeadapteroutbound.NewGeocoderAPI(),
			geocodeadapteroutbound.NewPostgresAddressCache(pool),
			geocodeadapteroutbound.NewPostgresItemGeoWriter(pool),
			events,
		)
		err = geocodeadapterinbound.NewAMQPConsumer(q, svc).Run(ctx)

	case "ai":
		providers := []aidomain.Provider{
			{Name: "openai", Classifier: aiadapteroutbound.NewOpenAIClassifier("")},
			{Name: "alibaba", Classifier: aiadapteroutbound.NewAlibabaClassifier("")},
			{Name: "mock", Classifier: aiadapteroutbound.NewMockClassifier()}, // fallback по умолчанию, без ключей
		}
		breakerStore := aiadapteroutbound.NewPostgresBreakerStore(pool) // per-provider, переживает рестарт
		svc := aidomain.NewService(
			gate, providers, breakerStore,
			aiadapteroutbound.NewPostgresItemAIWriter(pool),
			events,
		)
		err = aiadapterinbound.NewAMQPConsumer(q, svc).Run(ctx)

	case "cleanup":
		// Без domain/adapter-расщепления — см. internal/cleanup/purge.go doc-comment.
		err = cleanup.NewTimer(pool, core.RawItemTTL, time.Hour).Run(ctx)

	default:
		// Недостижимо на практике: cobra.ExactValidArgs(1) + ValidArgs
		// (main.go) уже отклонили неизвестную стадию ДО вызова runWorker.
		// Оставлено defensive — дешёвая подстраховка, не полагается молча
		// на то, что единственный вызывающий (main.go) всегда прав.
		return fmt.Errorf("unknown worker stage: %s", stage)
	}

	if err != nil && err != context.Canceled {
		return fmt.Errorf("worker %s: %w", stage, err)
	}
	return nil
}
