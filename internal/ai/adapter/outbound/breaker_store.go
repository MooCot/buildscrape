package outbound

import (
	"context"
	"fmt"
	"time"

	"buildscrape/internal/ai/domain"
	"buildscrape/internal/platform/db"
)

// PostgresBreakerStore — key-value состояние breaker per provider,
// переживает рестарт процесса (CLAUDE.md §8). Хранит state И opened_at
// (не только state) — без второго гидратация Open-breaker после рестарта
// ломает cooldown-проверку (BreakerSnapshot doc-comment, ai/domain/ports.go
// — найдено юнит-тестом TestClassifyItem_BreakerStateSurvivesRestart, не
// спроектировано заранее).
type PostgresBreakerStore struct{ pool db.Pool }

func NewPostgresBreakerStore(p db.Pool) *PostgresBreakerStore { return &PostgresBreakerStore{pool: p} }

func (s *PostgresBreakerStore) Load(ctx context.Context, provider string) (domain.BreakerSnapshot, error) {
	rows, err := s.pool.Query(ctx, `SELECT state, opened_at FROM ai_breaker_state WHERE provider = $1`, provider)
	if err != nil {
		return domain.BreakerSnapshot{}, fmt.Errorf("load breaker state: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		// Не найдено — zero value (State=Closed, OpenedAt=zero time), тот
		// же default, что и у только что созданного CircuitBreaker.
		return domain.BreakerSnapshot{}, rows.Err()
	}
	var stateStr string
	var openedAt *time.Time // opened_at NULL, пока breaker ни разу не открывался
	if err := rows.Scan(&stateStr, &openedAt); err != nil {
		return domain.BreakerSnapshot{}, fmt.Errorf("load breaker state: scan: %w", err)
	}
	snap := domain.BreakerSnapshot{State: stringToState(stateStr)}
	if openedAt != nil {
		snap.OpenedAt = *openedAt
	}
	return snap, rows.Err()
}

func (s *PostgresBreakerStore) Save(ctx context.Context, provider string, snap domain.BreakerSnapshot) error {
	var openedAt *time.Time
	if !snap.OpenedAt.IsZero() {
		t := snap.OpenedAt
		openedAt = &t
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ai_breaker_state (provider, state, opened_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (provider) DO UPDATE SET
			state     = EXCLUDED.state,
			opened_at = EXCLUDED.opened_at
	`, provider, stateToString(snap.State), openedAt)
	if err != nil {
		return fmt.Errorf("save breaker state: %w", err)
	}
	return nil
}

// state↔string — сериализационная деталь адаптера (миграция хранит text,
// не int), не домена: domain.State остаётся простым enum, ничего не знает
// про то, как его текстовое представление выглядит в БД.
func stateToString(s domain.State) string {
	switch s {
	case domain.Open:
		return "open"
	case domain.HalfOpen:
		return "half_open"
	default:
		return "closed"
	}
}

func stringToState(s string) domain.State {
	switch s {
	case "open":
		return domain.Open
	case "half_open":
		return domain.HalfOpen
	default:
		return domain.Closed
	}
}
