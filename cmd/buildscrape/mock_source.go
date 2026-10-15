package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"buildscrape/internal/mocksource"

	"github.com/spf13/cobra"
)

// newMockSourceCmd — демо-источник (CLAUDE.md §2). НЕ роль pipeline, а
// фикстура: живёт в том же бинарнике только ради того, чтобы docker compose
// поднимал его тем же образом, что и остальные роли.
func newMockSourceCmd() *cobra.Command {
	var (
		addr string
		opts mocksource.Options
	)
	cmd := &cobra.Command{
		Use:   "mock-source",
		Short: "демо-источник: фейковые объявления с нестабильным HTML, 429 и CAPTCHA-заглушками",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMockSource(addr, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&addr, "addr", ":9090", "адрес HTTP-сервера")
	f.IntVar(&opts.Count, "listings", 300, "сколько объявлений")
	f.IntVar(&opts.PageSize, "page-size", 50, "объявлений на страницу индекса")
	f.Float64Var(&opts.RateLimitProb, "rate-limit-prob", 0.05, "вероятность 429 на запрос")
	f.Float64Var(&opts.ServerErrProb, "server-error-prob", 0.02, "вероятность 503 на запрос")
	f.Float64Var(&opts.CaptchaProb, "captcha-prob", 0.03, "вероятность страницы-заглушки вместо объявления")
	f.DurationVar(&opts.MaxDelay, "max-delay", 300*time.Millisecond, "максимальная случайная задержка ответа")
	f.Int64Var(&opts.Seed, "seed", time.Now().UnixNano(), "seed хаоса (фиксировать для воспроизводимого прогона)")
	return cmd
}

func runMockSource(addr string, opts mocksource.Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	srv := &http.Server{Addr: addr, Handler: mocksource.New(opts).Handler()}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	fmt.Printf("mock-source: %d объявлений на %s (429=%.0f%% 503=%.0f%% captcha=%.0f%%)\n",
		opts.Count, addr, opts.RateLimitProb*100, opts.ServerErrProb*100, opts.CaptchaProb*100)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
