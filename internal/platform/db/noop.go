package db

import "context"

// NoopPool — заглушка Pool для composition root, когда реальная БД не
// поднята (демо/юнит-уровень) — см. queue.NoopClient doc-comment, тот же
// принцип. PgxPool (pgx_pool.go) — реальная реализация.
type NoopPool struct{}

func NewNoopPool() *NoopPool { return &NoopPool{} }

func (p *NoopPool) Exec(ctx context.Context, sql string, args ...any) (int64, error) { return 0, nil }

func (p *NoopPool) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return &noopRows{}, nil
}

func (p *NoopPool) Close() {}

type noopRows struct{}

func (r *noopRows) Next() bool             { return false }
func (r *noopRows) Scan(dest ...any) error { return nil }
func (r *noopRows) Close()                 {}
func (r *noopRows) Err() error             { return nil }
