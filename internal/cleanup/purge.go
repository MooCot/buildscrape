// Package cleanup — F10: TTL-очистка RawItem после успешного parsed.
//
// Сознательно БЕЗ domain/adapter-расщепления, в отличие от остальных 6
// хексагонов. Модель сама пометила эту роль как отличную от pipeline-стадий
// («не одна из шести pipeline-стадий и не admin-действие, а отдельная
// housekeeping-роль», MODEL-buildscrape.md §1.6) — здесь нет ни гейта, ни
// нескольких независимых писателей, ни swappable внешнего провайдера,
// который стоило бы прятать за портом ради теста без инфраструктуры: вся
// логика — один предикат (status='parsed' ∧ fetched_at < now()-ttl) и один
// вызов репозитория. Полноценный хексагон (5 файлов, Service-интерфейс на
// единственную реализацию и единственного вызывающего) был бы весом больше
// самой логики — см. обсуждение "не переусложнил ли я хексагоналкой".
//
// Если позже здесь появится реальное решение (например: разные TTL по
// причине завершения, эскалация на failed после N попыток, метрика
// "сколько всего почищено за сутки") — это будет сигналом вернуть
// domain/ports-расщепление, не раньше.
package cleanup

import (
	"context"
	"time"

	"buildscrape/internal/platform/db"
)

// Purge — удаляет RawItem с status='parsed' старше ttl. Единственная
// реализация, единственный вызывающий (Timer, см. timer.go) — интерфейс
// здесь не даёт тестируемости, которой уже не даёт прямой вызов с fake
// db.Pool (тот же fake-порт, без лишнего слоя абстракции над ним).
func Purge(ctx context.Context, pool db.Pool, ttl time.Duration) (purged int, err error) {
	// Cutoff считается в Go (time.Now().Add(-ttl)), не в SQL (`now() -
	// $1::interval`) — избегает завязки на формат Postgres INTERVAL для
	// сериализации time.Duration; timestamptz-параметр однозначен и
	// переносим между драйверами.
	cutoff := time.Now().Add(-ttl)
	n, err := pool.Exec(ctx, `DELETE FROM raw_items WHERE status = 'parsed' AND fetched_at < $1`, cutoff)
	return int(n), err
}
