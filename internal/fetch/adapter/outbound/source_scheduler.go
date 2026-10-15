package outbound

import (
	"context"
	"fmt"
	"time"

	"buildscrape/internal/platform/db"
	shareddomain "buildscrape/internal/shared/domain"
)

// PostgresSourceScheduler — реализует fetch/domain.SourceScheduler.
//
// ClaimDue выбирает и помечает источники ОДНИМ оператором, поэтому два
// Fetch-воркера (масштабируются --scale) не заберут один и тот же источник:
// перепроверка "пора ли" делается в ON CONFLICT ... DO UPDATE ... WHERE, то
// есть под блокировкой строки — конкурент, пришедший вторым, видит уже
// сдвинутый next_poll_at, его WHERE ложно, и строка в RETURNING не попадает.
// Источник, у которого строки в source_poll_state ещё нет, забирается
// вставкой; гонка двух вставок одного ключа сводится к тому же ON CONFLICT.
//
// Фильтра по фазе (enabled/archived) НЕТ намеренно — см. domain.SourceScheduler.
//
// НЕ прогонялось против живого Postgres (юнит-уровня у SQL нет) — первая
// реальная проверка при `docker compose up`.
type PostgresSourceScheduler struct{ pool db.Pool }

func NewPostgresSourceScheduler(p db.Pool) *PostgresSourceScheduler {
	return &PostgresSourceScheduler{pool: p}
}

func (s *PostgresSourceScheduler) ClaimDue(ctx context.Context, now time.Time) ([]shareddomain.Source, error) {
	rows, err := s.pool.Query(ctx, `
		WITH claimed AS (
			INSERT INTO source_poll_state (source_id, next_poll_at)
			SELECT s.id, $1::timestamptz + s.poll_interval_sec * interval '1 second'
			FROM sources s
			LEFT JOIN source_poll_state p ON p.source_id = s.id
			WHERE p.next_poll_at IS NULL OR p.next_poll_at <= $1
			ON CONFLICT (source_id) DO UPDATE SET next_poll_at = EXCLUDED.next_poll_at
			WHERE source_poll_state.next_poll_at <= $1
			RETURNING source_id
		)
		SELECT s.id, s.name, s.base_url, s.poll_interval_sec, s.enabled, s.archived
		FROM sources s
		JOIN claimed c ON c.source_id = s.id
	`, now)
	if err != nil {
		return nil, fmt.Errorf("claim due sources: %w", err)
	}
	defer rows.Close()

	var out []shareddomain.Source
	for rows.Next() {
		var src shareddomain.Source
		if err := rows.Scan(&src.ID, &src.Name, &src.BaseURL, &src.PollIntervalSec, &src.Enabled, &src.Archived); err != nil {
			return nil, fmt.Errorf("claim due sources: scan: %w", err)
		}
		out = append(out, src)
	}
	return out, rows.Err()
}
