package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxPool — реальная реализация Pool поверх pgxpool.Pool (CLAUDE.md §6).
type PgxPool struct {
	pool *pgxpool.Pool
}

// NewPgxPool — открывает пул + Ping сразу (не откладывает обнаружение
// недоступной БД до первого реального запроса в середине обработки
// сообщения — тот же принцип, что queue.Dial: составной root решает
// политику старта, а не первый вызывающий код узнаёт о проблеме случайно).
func NewPgxPool(ctx context.Context, dsn string) (*PgxPool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PgxPool{pool: pool}, nil
}

func (p *PgxPool) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := p.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (p *PgxPool) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	rows, err := p.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return &pgxRows{rows: rows}, nil
}

func (p *PgxPool) Close() { p.pool.Close() }

// pgxRows — тонкая обёртка pgx.Rows под интерфейс Rows (db/pool.go) —
// нужна, чтобы outbound-репозитории зависели от db.Rows (свой минимальный
// контракт), не напрямую от pgx.Rows (иначе смысл порта теряется —
// domain/adapter граница держится именно на том, что platform/db прячет
// конкретный драйвер).
type pgxRows struct {
	rows pgx.Rows
}

func (r *pgxRows) Next() bool             { return r.rows.Next() }
func (r *pgxRows) Scan(dest ...any) error { return r.rows.Scan(dest...) }
func (r *pgxRows) Close()                 { r.rows.Close() }
func (r *pgxRows) Err() error             { return r.rows.Err() }
