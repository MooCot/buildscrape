package outbound

import (
	"context"
	"fmt"

	"buildscrape/internal/geocode/domain"
	"buildscrape/internal/platform/db"
	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// PostgresAddressCache — реализует geocode/domain.AddressCache, переиспользуя
// items как хранилище кеша (уже геокодированные строки), НЕ отдельная
// таблица: тот же физический факт (lat/lng по адресу) уже пишет
// PostgresItemGeoWriter.SaveGeo ниже — заводить вторую таблицу означало бы
// хранить один факт в двух местах (тот класс дублирования, которого
// избегали весь этот проект).
type PostgresAddressCache struct{ pool db.Pool }

func NewPostgresAddressCache(p db.Pool) *PostgresAddressCache { return &PostgresAddressCache{pool: p} }

func (c *PostgresAddressCache) Get(ctx context.Context, addr string) (domain.GeoResult, bool, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT lat, lng FROM items
		WHERE address = $1 AND geocoded_at IS NOT NULL
		LIMIT 1
	`, addr)
	if err != nil {
		return domain.GeoResult{}, false, fmt.Errorf("address cache get: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return domain.GeoResult{}, false, rows.Err()
	}
	var r domain.GeoResult
	if err := rows.Scan(&r.Lat, &r.Lng); err != nil {
		return domain.GeoResult{}, false, fmt.Errorf("address cache get: scan: %w", err)
	}
	return r, true, rows.Err()
}

// Set — намеренно no-op: "запись в кеш" физически ПРОИСХОДИТ через
// PostgresItemGeoWriter.SaveGeo (делает текущий item геокодированным и тем
// самым доступным как источник кеша для следующего item с тем же адресом,
// см. Get выше) — отдельная запись здесь дублировала бы тот же факт.
func (c *PostgresAddressCache) Set(ctx context.Context, addr string, r domain.GeoResult) error {
	return nil
}

type PostgresItemGeoWriter struct{ pool db.Pool }

func NewPostgresItemGeoWriter(p db.Pool) *PostgresItemGeoWriter {
	return &PostgresItemGeoWriter{pool: p}
}

func (w *PostgresItemGeoWriter) SaveGeo(ctx context.Context, itemID uuid.UUID, r domain.GeoResult) error {
	n, err := w.pool.Exec(ctx, `
		UPDATE items SET lat = $1, lng = $2, geocoded_at = now() WHERE id = $3
	`, r.Lat, r.Lng, itemID)
	if err != nil {
		return fmt.Errorf("save geo: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("save geo: item %s: %w", itemID, shareddomain.ErrNotFound)
	}
	return nil
}
