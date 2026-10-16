package outbound

import (
	"context"
	"errors"
	"testing"

	"buildscrape/internal/platform/db"
	"buildscrape/internal/platform/queue"

	"github.com/google/uuid"
)

type fakePool struct {
	execSQL  string
	execArgs []any
	execErr  error
	execN    int
}

func (p *fakePool) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	p.execN++
	p.execSQL = sql
	p.execArgs = args
	return 1, p.execErr
}

func (p *fakePool) Query(ctx context.Context, sql string, args ...any) (db.Rows, error) {
	return nil, errors.New("not used by EventRecorder")
}

func (p *fakePool) Close() {}

type fakeConn struct {
	publishN   int
	publishErr error
}

func (c *fakeConn) Publish(ctx context.Context, exchange, routingKey string, body []byte) error {
	c.publishN++
	return c.publishErr
}

func (c *fakeConn) Consume(ctx context.Context, queueName string, prefetch int) (<-chan queue.Delivery, error) {
	return nil, errors.New("not used by EventRecorder")
}

func (c *fakeConn) QueueLength(ctx context.Context, queueName string) (int, error) {
	return 0, errors.New("not used by EventRecorder")
}

type fakeRecorder struct {
	incN            int
	lastStage       string
	lastStatus      string
	observeDuration int
}

func (f *fakeRecorder) IncProcessed(stage, status string) {
	f.incN++
	f.lastStage, f.lastStatus = stage, status
}

func (f *fakeRecorder) ObserveDuration(stage string, seconds float64) { f.observeDuration++ }
func (f *fakeRecorder) SetQueueLength(queue string, n int)            {}

func TestEventRecorder_WritesRowAndPublishes(t *testing.T) {
	pool := &fakePool{}
	conn := &fakeConn{}
	rec := &fakeRecorder{}
	r := NewEventRecorder(pool, conn, rec)

	if err := r.PublishStageEvent(context.Background(), uuid.New(), "parse", "done", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pool.execN != 1 {
		t.Fatalf("expected exactly one INSERT, got %d", pool.execN)
	}
	if conn.publishN != 1 {
		t.Fatalf("expected exactly one AMQP publish, got %d", conn.publishN)
	}
	if rec.incN != 1 || rec.lastStage != "parse" || rec.lastStatus != "done" {
		t.Fatalf("expected IncProcessed(parse, done) exactly once, got n=%d stage=%q status=%q", rec.incN, rec.lastStage, rec.lastStatus)
	}
}

// Запись в pipeline_events — обязательная часть результата стадии (MODEL
// §1.5 правка): ошибка INSERT обязана провалить весь вызов (вызывающий
// stage-domain возвращает её дальше, сообщение уходит на retry/DLQ), не
// глушится молча.
func TestEventRecorder_DBErrorPropagates(t *testing.T) {
	pool := &fakePool{execErr: errors.New("connection reset")}
	conn := &fakeConn{}
	rec := &fakeRecorder{}
	r := NewEventRecorder(pool, conn, rec)

	err := r.PublishStageEvent(context.Background(), uuid.New(), "ai", "error", "boom")
	if err == nil {
		t.Fatal("expected DB error to propagate, got nil")
	}
	if conn.publishN != 0 {
		t.Fatalf("expected no AMQP publish after a failed DB write, got %d", conn.publishN)
	}
	if rec.incN != 0 {
		t.Fatalf("expected IncProcessed NOT called when the DB write fails, got %d calls", rec.incN)
	}
}

// pipeline.events (fanout, живой дашборд) — best-effort рядом с журналом,
// не вместо него: сбой публикации НЕ должен ретраить уже успешно
// залогированную стадию (CLAUDE.md §4.3 — подписчики сами заводят очередь;
// их отсутствие/сбой — не причина дублировать работу стадии).
func TestEventRecorder_PublishErrorIsSwallowed(t *testing.T) {
	pool := &fakePool{}
	conn := &fakeConn{publishErr: errors.New("no connection")}
	r := NewEventRecorder(pool, conn, &fakeRecorder{})

	if err := r.PublishStageEvent(context.Background(), uuid.New(), "media", "done", ""); err != nil {
		t.Fatalf("expected AMQP publish failure to be swallowed, got error: %v", err)
	}
	if pool.execN != 1 {
		t.Fatalf("expected the DB write to still happen, got %d", pool.execN)
	}
}

func TestEventRecorder_EmptyErrorBecomesNilArg(t *testing.T) {
	pool := &fakePool{}
	r := NewEventRecorder(pool, &fakeConn{}, &fakeRecorder{})

	if err := r.PublishStageEvent(context.Background(), uuid.New(), "geocode", "done", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// INSERT INTO pipeline_events (item_id, stage, status, error) VALUES ($1,$2,$3,$4)
	if len(pool.execArgs) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(pool.execArgs), pool.execArgs)
	}
	if pool.execArgs[3] != nil {
		t.Fatalf("expected NULL (nil) error arg for empty errMsg, got %v", pool.execArgs[3])
	}
}
