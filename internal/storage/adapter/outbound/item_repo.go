package outbound

import (
	"context"
	"fmt"

	"buildscrape/internal/platform/db"
	shareddomain "buildscrape/internal/shared/domain"
	"buildscrape/internal/storage/domain"

	"github.com/google/uuid"
)

// PostgresItemReader — F6, публичная выдача. List/Get оба JOIN'ят sources
// и фильтруют по "не заблокирован" ПРЯМО В SQL (`i.blocked_reason IS NULL
// AND NOT s.archived AND s.enabled`) — та же формула, что computePhase()
// в source_repo.go, переписанная как SQL-текст, не вызов функции: bulk-
// листинг через per-item IsBlocked()/Phase() был бы N+1-запросов на каждый
// список. Это ЕДИНСТВЕННОЕ осознанно принятое исключение из "не
// дублировать гейт-условие" во всём этом проекте — если C6/C8 когда-либо
// изменится, обе стороны (эта строка и computePhase) придётся поправить
// руками, компилятор не свяжет их. Get() фильтрует ТЕМ ЖЕ условием, не
// только List() — иначе заблокированный item был бы скрыт из списка, но
// доступен напрямую по id (утечка мимо гейта).
type PostgresItemReader struct{ pool db.Pool }

func NewPostgresItemReader(p db.Pool) *PostgresItemReader { return &PostgresItemReader{pool: p} }

const notBlockedJoinCondition = `i.blocked_reason IS NULL AND NOT s.archived AND s.enabled`

func (r *PostgresItemReader) List(ctx context.Context, filter domain.ItemFilter) ([]domain.ItemView, error) {
	query := `
		SELECT i.id, i.title, i.description, i.price, i.lat, i.lng, i.ai_category
		FROM items i
		JOIN sources s ON s.id = i.source_id
		WHERE ` + notBlockedJoinCondition

	var args []any
	if filter.SourceID != uuid.Nil {
		args = append(args, filter.SourceID)
		query += fmt.Sprintf(" AND i.source_id = $%d", len(args))
	}
	if filter.Query != "" {
		args = append(args, "%"+filter.Query+"%")
		query += fmt.Sprintf(" AND i.title ILIKE $%d", len(args))
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list items: %w", err)
	}
	defer rows.Close()

	var out []domain.ItemView
	for rows.Next() {
		v, err := scanItemView(rows)
		if err != nil {
			return nil, fmt.Errorf("list items: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *PostgresItemReader) Get(ctx context.Context, id uuid.UUID) (domain.ItemView, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.id, i.title, i.description, i.price, i.lat, i.lng, i.ai_category
		FROM items i
		JOIN sources s ON s.id = i.source_id
		WHERE i.id = $1 AND `+notBlockedJoinCondition, id)
	if err != nil {
		return domain.ItemView{}, fmt.Errorf("get item: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return domain.ItemView{}, fmt.Errorf("get item: %w", err)
		}
		return domain.ItemView{}, fmt.Errorf("get item %s: %w", id, shareddomain.ErrNotFound) // нет ИЛИ заблокирован — снаружи неразличимо
	}
	v, err := scanItemView(rows)
	if err != nil {
		return domain.ItemView{}, fmt.Errorf("get item: %w", err)
	}
	return v, rows.Err()
}

// scanItemView — общий Scan для List/Get, чтобы порядок колонок в SELECT и
// порядок Scan-аргументов не расходился между двумя копиями (тот же
// текстовый SELECT-список тоже вручную держится идентичным в обоих
// запросах выше — единственное реальное текстовое дублирование внутри
// этого файла, помимо задокументированного notBlockedJoinCondition).
func scanItemView(rows db.Rows) (domain.ItemView, error) {
	var v domain.ItemView
	var aiCategory *string
	// lat/lng сканируются прямо в указатели ItemView: NULL → nil ("не
	// геокодирован"), а не 0.0 — точка (0,0) на карте это не "нет данных".
	if err := rows.Scan(&v.ID, &v.Title, &v.Description, &v.Price, &v.Lat, &v.Lng, &aiCategory); err != nil {
		return domain.ItemView{}, fmt.Errorf("scan: %w", err)
	}
	if aiCategory != nil {
		v.AICategory = *aiCategory
	}
	return v, nil
}
