package outbound

import (
	"context"
	"fmt"

	"buildscrape/internal/platform/db"
	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// PostgresSourceRepository реализует shareddomain.SourceRepository —
// единственный писатель всех граней Source. Живёт в storage/adapter, а не
// в internal/shared, потому что shared/domain — только порты (интерфейсы),
// реализации — у стадии-владельца (CLAUDE.md §8, internal/shared/domain).
type PostgresSourceRepository struct{ pool db.Pool }

func NewPostgresSourceRepository(p db.Pool) *PostgresSourceRepository {
	return &PostgresSourceRepository{pool: p}
}

func (r *PostgresSourceRepository) Create(ctx context.Context, s shareddomain.Source) (shareddomain.Source, error) {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sources (id, name, base_url, poll_interval_sec, enabled, archived)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, s.ID, s.Name, s.BaseURL, s.PollIntervalSec, s.Enabled, s.Archived)
	if err != nil {
		return shareddomain.Source{}, fmt.Errorf("create source: %w", err)
	}
	return s, nil
}

func (r *PostgresSourceRepository) SetEnabled(ctx context.Context, sourceID uuid.UUID, enabled bool) error {
	n, err := r.pool.Exec(ctx, `UPDATE sources SET enabled = $1 WHERE id = $2`, enabled, sourceID)
	if err != nil {
		return fmt.Errorf("set enabled: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("set enabled: source %s: %w", sourceID, shareddomain.ErrNotFound)
	}
	return nil
}

// Archive — одностороннее (нет archived→active), UPDATE ... SET
// archived=true — сама СУБД никак это не запрещает при повторном вызове
// (идемпотентно: архивировать уже заархивированный источник — no-op, не
// ошибка); одностороннесть — дисциплина на уровне отсутствия обратного
// метода в порту SourceRepository, не ограничение в этом SQL.
func (r *PostgresSourceRepository) Archive(ctx context.Context, sourceID uuid.UUID) error {
	n, err := r.pool.Exec(ctx, `UPDATE sources SET archived = true WHERE id = $1`, sourceID)
	if err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("archive: source %s: %w", sourceID, shareddomain.ErrNotFound)
	}
	return nil
}

func (r *PostgresSourceRepository) Phase(ctx context.Context, sourceID uuid.UUID) (shareddomain.SourcePhase, error) {
	rows, err := r.pool.Query(ctx, `SELECT s.archived, s.enabled FROM sources s WHERE s.id = $1`, sourceID)
	if err != nil {
		return "", fmt.Errorf("phase: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("phase: %w", err)
		}
		return "", fmt.Errorf("phase: source %s: %w", sourceID, shareddomain.ErrNotFound)
	}
	var archived, enabled bool
	if err := rows.Scan(&archived, &enabled); err != nil {
		return "", fmt.Errorf("phase: scan: %w", err)
	}
	return computePhase(archived, enabled), rows.Err()
}

// computePhase — единственная Go-реализация приоритета C6 (archived
// приоритетнее enabled). item_repo.go List()/Get() дублируют ЭТУ ЖЕ
// логику как SQL-текст (не вызывают эту функцию — SQL и Go нельзя
// разделить буквально) — единственное осознанно принятое исключение из
// "не дублировать гейт-условие" в этом проекте, ради bulk-запроса без N+1
// (см. комментарий там). Если приоритет когда-либо изменится — обе стороны
// придётся поправить руками, компилятор не свяжет их.
func computePhase(archived, enabled bool) shareddomain.SourcePhase {
	if archived {
		return shareddomain.SourcePhaseArchived
	}
	if enabled {
		return shareddomain.SourcePhaseActive
	}
	return shareddomain.SourcePhaseSuspended
}

// PostgresItemGateChecker реализует shareddomain.ItemGateChecker (C8).
// IsBlocked консультирует sources.Phase БУКВАЛЬНО (тот же вызов, что и у
// Fetch/P1) — не переписывает C6 отдельной SQL-строкой: единственный
// случай в этом файле, где логика переиспользуется кодом, а не
// SQL-текстом-копией (направление зависимости ItemGateChecker →
// SourceRepository.Phase, MODEL-buildscrape.md §9, СР-storage).
type PostgresItemGateChecker struct {
	pool    db.Pool
	sources shareddomain.SourceRepository
}

func NewPostgresItemGateChecker(p db.Pool, sources shareddomain.SourceRepository) *PostgresItemGateChecker {
	return &PostgresItemGateChecker{pool: p, sources: sources}
}

func (c *PostgresItemGateChecker) IsBlocked(ctx context.Context, itemID uuid.UUID) (bool, error) {
	rows, err := c.pool.Query(ctx, `SELECT blocked_reason, source_id FROM items WHERE id = $1`, itemID)
	if err != nil {
		return false, fmt.Errorf("is blocked: %w", err)
	}
	var (
		blockedReason *string
		sourceID      uuid.UUID
		found         = rows.Next()
	)
	if found {
		err = rows.Scan(&blockedReason, &sourceID)
	}
	scanErr := rows.Err()
	rows.Close()

	if err != nil {
		return false, fmt.Errorf("is blocked: scan: %w", err)
	}
	if !found {
		if scanErr != nil {
			return false, fmt.Errorf("is blocked: %w", scanErr)
		}
		return false, fmt.Errorf("is blocked: item %s: %w", itemID, shareddomain.ErrNotFound)
	}
	if scanErr != nil {
		return false, scanErr
	}
	if blockedReason != nil {
		return true, nil // C8 — своя причина уже достаточна, Phase спрашивать не нужно
	}

	phase, err := c.sources.Phase(ctx, sourceID)
	if err != nil {
		return false, fmt.Errorf("is blocked: phase: %w", err)
	}
	return phase != shareddomain.SourcePhaseActive, nil
}
