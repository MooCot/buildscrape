// buildscrape — один Go-бинарник, несколько ролей (CLAUDE.md §1).
//
// Использование:
//
//	buildscrape serve
//	buildscrape worker fetch|parse|media|geocode|ai|cleanup
package main

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// cobra уже напечатала ошибку в stderr сама (SilenceErrors не
		// установлен) — здесь только код возврата процесса.
		os.Exit(1)
	}
}

var knownStages = []string{"fetch", "parse", "media", "geocode", "ai", "cleanup"}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "buildscrape",
		Short: "Event-driven pipeline — сбор/обогащение/выдача данных (CLAUDE.md §1)",
		// SilenceUsage — ПРОВЕРЕНО эмпирически (`worker bogus`), не
		// предположено по документации: подавляет usage-блок одинаково при
		// ЛЮБОЙ ошибке Execute(), не различая "неверно вызвал команду"
		// (ExactValidArgs отклонил стадию) от "команда вызвана верно, упала
		// внутри RunE" (queue.Dial недоступен и т.п.) — в cobra это один и
		// тот же флаг на оба случая, разделить их без ручной обработки
		// ошибки в main() нельзя. Здесь это устраивает: короткое сообщение
		// об ошибке важнее повторного полотна usage под ним в обоих
		// случаях.
		SilenceUsage: true,
	}
	root.AddCommand(newServeCmd(), newWorkerCmd(), newMockSourceCmd(), newAdminCmd())
	return root
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "HTTP API + admin (F6/F7/F8/F9)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe()
		},
	}
}

func newWorkerCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "worker <stage>",
		Short: "воркер одной стадии pipeline: " + strings.Join(knownStages, "|"),
		// ExactValidArgs = ExactArgs(1) + OnlyValidArgs — неизвестная стадия
		// отклоняется ДО runWorker, тем же путём, что раньше делал default
		// в switch (см. runWorker doc-comment на defensive-fallback).
		Args:      cobra.ExactValidArgs(1),
		ValidArgs: knownStages,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorker(args[0])
		},
	}
}
