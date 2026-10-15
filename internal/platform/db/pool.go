// Package db — pgxpool-обёртка (CLAUDE.md §8/§6).
package db

import "context"

// Pool — контракт, которым пользуются outbound-репозитории каждой стадии;
// держит пул соединений НА ПРОЦЕСС (см. ТЕОРИЯ-УГЛУБЛЁННО.md §3.5 — почему
// нельзя плодить соединение на каждый запрос при десятках воркеров).
//
// Exec возвращает rowsAffected (не только error) — НУЖНО реальным
// вызывающим: cleanup.Purge отчитывается, сколько RawItem реально удалено
// (не "команда прошла", а "что-то удалила"); SetEnabled/Archive/Block могут
// проверить rowsAffected==0 как сигнал "id не существует", не только
// "запрос не вернул ошибку" (запрос к несуществующему id в SQL — не error).
type Pool interface {
	Exec(ctx context.Context, sql string, args ...any) (rowsAffected int64, err error)
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Close()
}

// Rows — минимальный контракт результата запроса.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close()
	Err() error // ошибка итерации (сетевой обрыв посреди Next()) — отдельно от ошибки самого запроса
}
