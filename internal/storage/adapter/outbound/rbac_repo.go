package outbound

import (
	"context"
	"fmt"

	"buildscrape/internal/platform/db"
	"buildscrape/internal/storage/domain"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// PostgresRBACRepository реализует СРАЗУ domain.ApiKeyRepository и
// domain.RoleRepository — в отличие от PostgresSourceRepository/
// PostgresItemGateChecker (source_repo.go), которые разделены на два
// struct несмотря на общую таблицу sources: там это две концептуально
// РАЗНЫЕ задачи, здесь — одна и та же подсистема (api_keys/roles/
// api_key_roles органически связаны FK, не случайно колоцированы).
type PostgresRBACRepository struct{ pool db.Pool }

func NewPostgresRBACRepository(p db.Pool) *PostgresRBACRepository {
	return &PostgresRBACRepository{pool: p}
}

// Lookup — JOIN api_keys -> api_key_roles -> roles, разворачивает
// permissions каждой назначенной роли через unnest и объединяет DISTINCT:
// результат — уже готовый, дедуплицированный набор прав ключа по ВСЕМ его
// ролям (смысл RBAC именно в объединении, не в правах одной роли).
// LEFT JOIN LATERAL ... ON true — чтобы ключ без единой роли (или без
// единого права) дал одну строку с пустым набором, а не исчез из
// результата как при обычном INNER unnest. p::text ВНУТРИ array_agg —
// агрегат получается типа text[], не permission[]: pgx декодирует его в
// []string без какой-либо регистрации кастомного enum-типа на стороне
// Go (см. комментарий у CreateRole о соответствующей записи).
func (r *PostgresRBACRepository) Lookup(ctx context.Context, tokenHash string) (domain.Identity, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ak.id, ak.label,
		       COALESCE(array_agg(DISTINCT p::text) FILTER (WHERE p IS NOT NULL), '{}')
		FROM api_keys ak
		LEFT JOIN api_key_roles akr ON akr.api_key_id = ak.id
		LEFT JOIN roles ro ON ro.id = akr.role_id
		LEFT JOIN LATERAL unnest(ro.permissions) AS p ON true
		WHERE ak.token_hash = $1 AND ak.revoked_at IS NULL
		GROUP BY ak.id, ak.label
	`, tokenHash)
	if err != nil {
		return domain.Identity{}, fmt.Errorf("lookup api key: %w", err)
	}
	defer rows.Close()

	// Несуществующий и отозванный ключ дают одинаково "строки нет" —
	// ErrUnauthenticated в обоих случаях намеренно (domain/rbac.go).
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return domain.Identity{}, fmt.Errorf("lookup api key: %w", err)
		}
		return domain.Identity{}, domain.ErrUnauthenticated
	}

	var (
		id    uuid.UUID
		label string
		perms []string
	)
	if err := rows.Scan(&id, &label, &perms); err != nil {
		return domain.Identity{}, fmt.Errorf("lookup api key: scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return domain.Identity{}, fmt.Errorf("lookup api key: %w", err)
	}

	set := make(map[domain.Permission]bool, len(perms))
	for _, p := range perms {
		set[domain.Permission(p)] = true
	}
	return domain.Identity{KeyID: id, Label: label, Permissions: set}, nil
}

// Create — сначала проверяет, что ВСЕ roleNames существуют (одним
// запросом), и только потом пишет api_keys + api_key_roles. Не
// транзакционно (db.Pool не выставляет Begin — расширять общий для всех
// стадий порт ради одной CLI-команды избыточно): окно между двумя Exec —
// худший случай "ключ создан, но без части ролей", восстановимо через
// Revoke+повтор команды. Для демо-CLI оператора (не конкурентный горячий
// путь) это приемлемо.
func (r *PostgresRBACRepository) Create(ctx context.Context, label, tokenHash string, roleNames []string) (uuid.UUID, error) {
	if err := r.ensureRolesExist(ctx, roleNames); err != nil {
		return uuid.Nil, err
	}

	keyID := uuid.New()
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO api_keys (id, token_hash, label) VALUES ($1, $2, $3)
	`, keyID, tokenHash, label); err != nil {
		return uuid.Nil, fmt.Errorf("create api key: %w", err)
	}

	for _, name := range roleNames {
		if _, err := r.pool.Exec(ctx, `
			INSERT INTO api_key_roles (api_key_id, role_id) SELECT $1, id FROM roles WHERE name = $2
		`, keyID, name); err != nil {
			return uuid.Nil, fmt.Errorf("create api key: assign role %s: %w", name, err)
		}
	}
	return keyID, nil
}

func (r *PostgresRBACRepository) ensureRolesExist(ctx context.Context, roleNames []string) error {
	rows, err := r.pool.Query(ctx, `SELECT name FROM roles WHERE name = ANY($1::text[])`, roleNames)
	if err != nil {
		return fmt.Errorf("check roles: %w", err)
	}
	defer rows.Close()

	found := make(map[string]bool, len(roleNames))
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return fmt.Errorf("check roles: scan: %w", err)
		}
		found[n] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("check roles: %w", err)
	}
	for _, name := range roleNames {
		if !found[name] {
			return fmt.Errorf("create api key: unknown role %q", name)
		}
	}
	return nil
}

// Revoke — COALESCE(revoked_at, now()) делает повторный вызов идемпотентным
// (не двигает метку времени вперёд при повторной отзыве), при этом
// отличает "такого id нет вообще" (n=0 → ErrNotFound) от "уже был отозван"
// (n=1, идемпотентно).
func (r *PostgresRBACRepository) Revoke(ctx context.Context, id uuid.UUID) error {
	n, err := r.pool.Exec(ctx, `UPDATE api_keys SET revoked_at = COALESCE(revoked_at, now()) WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("revoke api key: %s: %w", id, shareddomain.ErrNotFound)
	}
	return nil
}

// CreateRole — $2::text[]::permission[]: сначала параметр типизируется как
// ОБЫЧНЫЙ text[] (pgx кодирует []string в него нативно, без регистрации
// кастомных типов), а ЗАТЕМ явно кастуется в permission[] — Postgres
// применяет этот каст поэлементно через стандартный ввод enum. Без этого
// двойного каста pgx потребовал бы знать OID кастомного массива enum
// заранее (регистрация типа при подключении) — лишняя сложность для
// одной CLI-команды.
func (r *PostgresRBACRepository) CreateRole(ctx context.Context, name string, perms []domain.Permission) (domain.Role, error) {
	ids := make([]string, len(perms))
	for i, p := range perms {
		ids[i] = string(p)
	}
	roleID := uuid.New()
	_, err := r.pool.Exec(ctx, `
		INSERT INTO roles (id, name, permissions) VALUES ($1, $2, $3::text[]::permission[])
	`, roleID, name, ids)
	if err != nil {
		return domain.Role{}, fmt.Errorf("create role: %w", err)
	}
	return domain.Role{ID: roleID, Name: name, Permissions: perms}, nil
}

// List — permissions::text[] (каст всего массива целиком, не поэлементно
// как в Lookup — здесь это права ОДНОЙ роли, не объединение нескольких, то
// же соображение с OID проще сработало на уровне всего столбца).
func (r *PostgresRBACRepository) List(ctx context.Context) ([]domain.Role, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, permissions::text[] FROM roles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()

	var result []domain.Role
	for rows.Next() {
		var (
			id    uuid.UUID
			name  string
			perms []string
		)
		if err := rows.Scan(&id, &name, &perms); err != nil {
			return nil, fmt.Errorf("list roles: scan: %w", err)
		}
		ps := make([]domain.Permission, len(perms))
		for i, p := range perms {
			ps[i] = domain.Permission(p)
		}
		result = append(result, domain.Role{ID: id, Name: name, Permissions: ps})
	}
	return result, rows.Err()
}
