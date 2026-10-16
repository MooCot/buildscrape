//go:build e2e

package e2e

import (
	"context"
	"errors"
	"testing"

	storageadapteroutbound "buildscrape/internal/storage/adapter/outbound"
	storagedomain "buildscrape/internal/storage/domain"
)

// TestRBAC_SeedRolesUnionAcrossKeyAndRevocation — E2E-7 (RBAC-редизайн
// admin API, замена единственного ADMIN_TOKEN, CLAUDE.md §8 "Не
// сделано"). Fake ApiKeyRepository в юнит-тестах (handlers_test.go) не
// существует вообще — там тестируется domain.Identity напрямую, минуя
// SQL. Здесь проверяется именно то, что fake не может воспроизвести:
//   - реальный ENUM permission[] в Postgres — запись через явный каст
//     $N::text[]::permission[] (migrations/0003_api_keys.sql) и чтение
//     обратно без регистрации кастомного типа на стороне pgx
//     (rbac_repo.go, p::text внутри array_agg / permissions::text[]);
//   - объединение прав ПО НЕСКОЛЬКИМ ролям через
//     JOIN+LEFT JOIN LATERAL unnest+array_agg DISTINCT — SQL-семантика,
//     не Go-цикл;
//   - реальный UNIQUE(token_hash) + revoked_at round-trip.
func TestRBAC_SeedRolesUnionAcrossKeyAndRevocation(t *testing.T) {
	pool := setupPostgres(t)
	ctx := context.Background()
	repo := storageadapteroutbound.NewPostgresRBACRepository(pool)

	t.Run("сидированные роли читаются с реальным ENUM permission[]", func(t *testing.T) {
		roles, err := repo.List(ctx)
		if err != nil {
			t.Fatalf("list roles: %v", err)
		}
		byName := make(map[string][]storagedomain.Permission, len(roles))
		for _, r := range roles {
			byName[r.Name] = r.Permissions
		}

		wantAdmin := []storagedomain.Permission{
			storagedomain.PermSourcesWrite, storagedomain.PermSourcesToggle, storagedomain.PermSourcesArchive,
		}
		if got := byName["admin"]; !samePermSet(got, wantAdmin) {
			t.Errorf("admin permissions = %v, want %v", got, wantAdmin)
		}
		if got := byName["operator"]; !samePermSet(got, []storagedomain.Permission{storagedomain.PermSourcesToggle}) {
			t.Errorf("operator permissions = %v, want [sources:toggle]", got)
		}
		if got := byName["publisher"]; !samePermSet(got, []storagedomain.Permission{storagedomain.PermSourcesWrite}) {
			t.Errorf("publisher permissions = %v, want [sources:write]", got)
		}
	})

	t.Run("ключ с двумя ролями получает ОБЪЕДИНЕНИЕ их прав, не права одной роли", func(t *testing.T) {
		keyID, err := repo.Create(ctx, "e2e-two-roles", storagedomain.HashToken("raw-two-roles"), []string{"operator", "publisher"})
		if err != nil {
			t.Fatalf("create api key: %v", err)
		}

		identity, err := repo.Lookup(ctx, storagedomain.HashToken("raw-two-roles"))
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if identity.KeyID != keyID {
			t.Errorf("identity.KeyID = %v, want %v", identity.KeyID, keyID)
		}
		if !identity.Has(storagedomain.PermSourcesToggle) || !identity.Has(storagedomain.PermSourcesWrite) {
			t.Errorf("permissions = %v — union operator+publisher должен дать toggle И write", identity.Permissions)
		}
		if identity.Has(storagedomain.PermSourcesArchive) {
			t.Error("archive не входит ни в operator, ни в publisher — не должно получиться само по себе")
		}

		if err := repo.Revoke(ctx, keyID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := repo.Lookup(ctx, storagedomain.HashToken("raw-two-roles")); !errors.Is(err, storagedomain.ErrUnauthenticated) {
			t.Errorf("lookup after revoke: err=%v, want ErrUnauthenticated", err)
		}
		// Идемпотентность: повторный Revoke на уже отозванном — не ошибка
		// (COALESCE(revoked_at, now()) в rbac_repo.go, не ErrNotFound).
		if err := repo.Revoke(ctx, keyID); err != nil {
			t.Errorf("second revoke: %v, want nil (idempotent)", err)
		}
	})

	t.Run("неизвестное имя роли отклоняется ДО записи ключа", func(t *testing.T) {
		_, err := repo.Create(ctx, "e2e-bad-role", storagedomain.HashToken("raw-bad-role"), []string{"operator", "no-such-role"})
		if err == nil {
			t.Fatal("ожидалась ошибка — роль no-such-role не существует")
		}
		if _, lookupErr := repo.Lookup(ctx, storagedomain.HashToken("raw-bad-role")); !errors.Is(lookupErr, storagedomain.ErrUnauthenticated) {
			t.Errorf("ключ не должен был создаться вообще: lookup err=%v, want ErrUnauthenticated", lookupErr)
		}
	})

	t.Run("CreateRole — запись нового ENUM-массива через $::text[]::permission[]", func(t *testing.T) {
		role, err := repo.CreateRole(ctx, "e2e-auditor", []storagedomain.Permission{storagedomain.PermSourcesToggle})
		if err != nil {
			t.Fatalf("create role: %v", err)
		}
		keyID, err := repo.Create(ctx, "e2e-auditor-key", storagedomain.HashToken("raw-auditor"), []string{role.Name})
		if err != nil {
			t.Fatalf("create api key for new role: %v", err)
		}
		identity, err := repo.Lookup(ctx, storagedomain.HashToken("raw-auditor"))
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if identity.KeyID != keyID || !identity.Has(storagedomain.PermSourcesToggle) || identity.Has(storagedomain.PermSourcesWrite) {
			t.Errorf("identity=%+v — новая роль, созданная через CreateRole, должна нести ровно те права, что ей дали", identity)
		}
	})
}

func samePermSet(got, want []storagedomain.Permission) bool {
	if len(got) != len(want) {
		return false
	}
	set := make(map[storagedomain.Permission]bool, len(got))
	for _, p := range got {
		set[p] = true
	}
	for _, p := range want {
		if !set[p] {
			return false
		}
	}
	return true
}
