package main

import (
	"context"
	"fmt"

	storageadapteroutbound "buildscrape/internal/storage/adapter/outbound"
	storagedomain "buildscrape/internal/storage/domain"

	"buildscrape/internal/platform/config"
	"buildscrape/internal/platform/db"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// newAdminCmd — RBAC bootstrap/управление (замена единственного
// ADMIN_TOKEN, CLAUDE.md "Не сделано" — "без ролей и ротации"). Работает
// напрямую с Postgres, минуя HTTP — иначе курица-яйцо: чтобы создать
// первый API-ключ через admin API, нужен уже существующий ключ. Тот же
// принцип, что mock-source (cmd/buildscrape/mock_source.go) — операторская
// команда живёт на этом же бинарнике, не отдельный инструмент.
func newAdminCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "admin",
		Short: "RBAC admin API: роли и API-ключи (напрямую в Postgres, минуя HTTP)",
	}
	root.AddCommand(
		newAdminCreateRoleCmd(),
		newAdminListRolesCmd(),
		newAdminCreateKeyCmd(),
		newAdminRevokeKeyCmd(),
	)
	return root
}

func newAdminCreateRoleCmd() *cobra.Command {
	var (
		name  string
		perms []string
	)
	cmd := &cobra.Command{
		Use:   "create-role",
		Short: "создать роль — именованный набор прав, управляемый отдельно от ключей",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminCreateRole(cmd.Context(), name, perms)
		},
	}
	f := cmd.Flags()
	f.StringVar(&name, "name", "", "имя роли (уникально)")
	f.StringSliceVar(&perms, "permissions", nil, "sources:write,sources:toggle,sources:archive")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("permissions")
	return cmd
}

func runAdminCreateRole(ctx context.Context, name string, rawPerms []string) error {
	perms := make([]storagedomain.Permission, len(rawPerms))
	for i, p := range rawPerms {
		if !storagedomain.ValidPermission(p) {
			return fmt.Errorf("unknown permission %q (valid: %v)", p, storagedomain.AllPermissions)
		}
		perms[i] = storagedomain.Permission(p)
	}

	repo, pool, err := newAdminRBACRepo(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	role, err := repo.CreateRole(ctx, name, perms)
	if err != nil {
		return err
	}
	fmt.Printf("role created: id=%s name=%s permissions=%v\n", role.ID, role.Name, role.Permissions)
	return nil
}

func newAdminListRolesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list-roles",
		Short: "список ролей и их прав",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminListRoles(cmd.Context())
		},
	}
}

func runAdminListRoles(ctx context.Context) error {
	repo, pool, err := newAdminRBACRepo(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	roles, err := repo.List(ctx)
	if err != nil {
		return err
	}
	for _, r := range roles {
		fmt.Printf("%s\t%s\t%v\n", r.ID, r.Name, r.Permissions)
	}
	return nil
}

func newAdminCreateKeyCmd() *cobra.Command {
	var (
		label string
		roles []string
	)
	cmd := &cobra.Command{
		Use:   "create-key",
		Short: "создать API-ключ с одной или несколькими ролями; raw-токен печатается РОВНО ОДИН РАЗ",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminCreateKey(cmd.Context(), label, roles)
		},
	}
	f := cmd.Flags()
	f.StringVar(&label, "label", "", `человекочитаемая метка ("ci-script", "ops-laptop")`)
	f.StringSliceVar(&roles, "role", nil, "имя роли, флаг можно повторить: --role=admin --role=publisher")
	_ = cmd.MarkFlagRequired("label")
	_ = cmd.MarkFlagRequired("role")
	return cmd
}

// runAdminCreateKey — raw-токен генерируется здесь и печатается РОВНО ОДИН
// РАЗ (storagedomain.NewRawToken, crypto/rand); в БД уходит только его
// SHA-256 (storagedomain.HashToken) — повторно получить raw из системы
// нельзя никак, только пересоздать ключ.
func runAdminCreateKey(ctx context.Context, label string, roles []string) error {
	raw, err := storagedomain.NewRawToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}

	repo, pool, err := newAdminRBACRepo(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	keyID, err := repo.Create(ctx, label, storagedomain.HashToken(raw), roles)
	if err != nil {
		return err
	}

	fmt.Printf("api key created: id=%s label=%s roles=%v\n", keyID, label, roles)
	fmt.Println()
	fmt.Println("СОХРАНИТЕ ЭТОТ ТОКЕН — больше он не будет показан (в БД хранится только его хэш):")
	fmt.Println(raw)
	return nil
}

func newAdminRevokeKeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke-key <id>",
		Short: "отозвать API-ключ (идемпотентно — повтор на уже отозванном не ошибка)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminRevokeKey(cmd.Context(), args[0])
		},
	}
}

func runAdminRevokeKey(ctx context.Context, rawID string) error {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return fmt.Errorf("invalid key id %q: %w", rawID, err)
	}

	repo, pool, err := newAdminRBACRepo(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := repo.Revoke(ctx, id); err != nil {
		return err
	}
	fmt.Printf("api key %s revoked\n", id)
	return nil
}

// newAdminRBACRepo — общее подключение для всех admin-сабкоманд; вызывающий
// обязан defer pool.Close() сам (возврат пары, не закрытие здесь — RunE
// выше пока не вернула ошибку/результат наружу).
func newAdminRBACRepo(ctx context.Context) (*storageadapteroutbound.PostgresRBACRepository, db.Pool, error) {
	cfg := config.Load()
	pool, err := newPool(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("db connect: %w", err)
	}
	return storageadapteroutbound.NewPostgresRBACRepository(pool), pool, nil
}
