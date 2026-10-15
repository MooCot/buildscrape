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

	queryService := storagedomain.NewItemQueryService(itemReader)
	adminService := storagedomain.NewSourceAdminService(sourceRepo)

	mux := http.NewServeMux()
	storageadapterinbound.NewHTTPHandler(queryService).Routes(mux)
	storageadapterinbound.NewAdminHandler(adminService, cfg.AdminToken).Routes(mux)

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
