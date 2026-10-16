package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"

	storageadapterinbound "buildscrape/internal/storage/adapter/inbound"
	storageadapteroutbound "buildscrape/internal/storage/adapter/outbound"
	storagedomain "buildscrape/internal/storage/domain"

	"buildscrape/internal/platform/config"
	"buildscrape/internal/platform/observability"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// runServe — composition root роли "serve" (HTTP API + admin, F6/F7/F8/F9).
// Возвращает error (не os.Exit внутри) — main.go/cobra решают код возврата
// процесса в одном месте, не разбросано по composition root. newPool —
// infra.go, общий с worker.go.
func runServe() error {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	pool, err := newPool(ctx, cfg.PostgresDSN)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer pool.Close()

	sourceRepo := storageadapteroutbound.NewPostgresSourceRepository(pool)
	itemReader := storageadapteroutbound.NewPostgresItemReader(pool)
	rbacRepo := storageadapteroutbound.NewPostgresRBACRepository(pool)

	queryService := storagedomain.NewItemQueryService(itemReader)
	adminService := storagedomain.NewSourceAdminService(sourceRepo)
	authSvc := storagedomain.NewApiKeyAuthenticator(rbacRepo)

	mux := http.NewServeMux()
	storageadapterinbound.NewHTTPHandler(queryService).Routes(mux)
	storageadapterinbound.NewAdminHandler(adminService, authSvc).Routes(mux)

	// F11/П-metrics (MODEL-buildscrape.md §8, CLAUDE.md §10.3) — только
	// serve опрашивает длину очередей, не каждый worker <stage> отдельно
	// (не дублировать нагрузку на брокер ради одной и той же цифры). Сам
	// AMQP-коннект под это — serve раньше вообще не трогал очередь.
	q, err := newQueueClient(cfg.RabbitMQURL)
	if err != nil {
		return fmt.Errorf("queue connect: %w", err)
	}
	if closer, ok := q.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	recorder := observability.NewPrometheusRecorder()
	mux.Handle("/metrics", promhttp.Handler())
	go runQueueLengthPoller(ctx, q, recorder)

	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background()) // graceful shutdown, ТЕОРИЯ-УГЛУБЛЁННО.md §8.3
	}()

	dbMode := "noop"
	if cfg.PostgresDSN != "" {
		dbMode = "real Postgres"
	}
	fmt.Printf("serve: listening on :8080 (db=%s)\n", dbMode)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
