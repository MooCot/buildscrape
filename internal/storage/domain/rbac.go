package domain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
)

// Permission — закрытый набор прав admin API, 1:1 с реальными хендлерами
// (F7/F8/F9, admin_handler.go). Значения буквально совпадают с ENUM
// permission в БД (migrations/0003_api_keys.sql) — рассинхрон отклонит
// сама СУБД при Create, не только комментарий здесь.
type Permission string

const (
	PermSourcesWrite   Permission = "sources:write"   // F7 — createSource
	PermSourcesToggle  Permission = "sources:toggle"  // F8 — setEnabled
	PermSourcesArchive Permission = "sources:archive" // F9 — archiveSource
)

// AllPermissions/ValidPermission — для CLI-валидации (`admin create-role
// --permissions=...`) ДО похода в БД: тот же принцип защиты, что
// decodeJSON.DisallowUnknownFields в admin_handler.go — опечатка должна
// дать понятную ошибку сразу, не ждать, пока её поймает ENUM-constraint
// Postgres с менее читаемым текстом.
var AllPermissions = []Permission{PermSourcesWrite, PermSourcesToggle, PermSourcesArchive}

func ValidPermission(s string) bool {
	for _, p := range AllPermissions {
		if string(p) == s {
			return true
		}
	}
	return false
}

// ErrUnauthenticated — токен неизвестен или отозван (HTTP 401). Отдельно
// от shareddomain.ErrNotFound намеренно: тот участвует в
// IsPermanent()/retry-классификации AMQP-стадий — смешивать с HTTP-
// периметром admin-auth было бы категориальной ошибкой (разные оси:
// "повторить ли доставку сообщения" vs "валиден ли токен запроса").
var ErrUnauthenticated = errors.New("unauthenticated")

// Role — именованный, управляемый отдельно от ключей набор прав
// (migrations/0003_api_keys.sql сидирует admin/operator/publisher).
type Role struct {
	ID          uuid.UUID
	Name        string
	Permissions []Permission
}

// Identity — результат успешной Authenticate: уже РАЗРЕШЁННЫЙ, объединённый
// по всем ролям набор прав. HTTP-слой (admin_handler.go) проверяет только
// Has(perm) — какая именно роль дала право, ему знать не нужно: индирекция
// Role->Permissions скрыта внутри ApiKeyRepository.Lookup.
type Identity struct {
	KeyID       uuid.UUID
	Label       string
	Permissions map[Permission]bool
}

func (id Identity) Has(p Permission) bool { return id.Permissions[p] }

// ApiKeyRepository — Lookup отдаёт ErrUnauthenticated одинаково для
// несуществующего И для отозванного ключа (revoked_at IS NOT NULL) —
// снаружи неразличимо намеренно: разница между "неверный токен" и "токен
// был верный, но отозван" не должна быть видна вызывающей стороне.
type ApiKeyRepository interface {
	Lookup(ctx context.Context, tokenHash string) (Identity, error)
	// Create — roleNames разрешаются в role_id через JOIN на roles.name
	// внутри реализации; неизвестное имя роли — ошибка (не молча
	// игнорируется).
	Create(ctx context.Context, label, tokenHash string, roleNames []string) (uuid.UUID, error)
	Revoke(ctx context.Context, id uuid.UUID) error
}

type RoleRepository interface {
	// CreateRole, не Create — на одном struct (PostgresRBACRepository)
	// реализованы оба порта сразу, а Go не допускает перегрузку методов:
	// ApiKeyRepository.Create и RoleRepository.Create столкнулись бы на
	// одном получателе с разными сигнатурами.
	CreateRole(ctx context.Context, name string, perms []Permission) (Role, error)
	List(ctx context.Context) ([]Role, error)
}

// ApiKeyAuthenticator — F-admin-auth. Не входит в MODEL-buildscrape.md:
// гейтит доступ СНАРУЖИ к F7/F8/F9, не состояние внутри пайплайна — та же
// классификация, что retry/DLQ (CLAUDE.md §8, "не hexagon, порты в
// стене"), только на HTTP-периметре, а не AMQP.
type ApiKeyAuthenticator interface {
	Authenticate(ctx context.Context, rawToken string) (Identity, error)
}

type apiKeyAuthenticator struct {
	repo ApiKeyRepository
}

func NewApiKeyAuthenticator(repo ApiKeyRepository) ApiKeyAuthenticator {
	return &apiKeyAuthenticator{repo: repo}
}

func (a *apiKeyAuthenticator) Authenticate(ctx context.Context, rawToken string) (Identity, error) {
	if rawToken == "" {
		return Identity{}, ErrUnauthenticated
	}
	return a.repo.Lookup(ctx, HashToken(rawToken))
}

// HashToken — SHA-256, не bcrypt: raw-токен — случайный высокоэнтропийный
// секрет (NewRawToken, 32 байта), не пароль человека. Он сравнивается по
// точному совпадению через индекс в БД, а не брутфорсится по словарю —
// замедление bcrypt здесь было бы чистым оверхедом без причины.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NewRawToken — 32 случайных байта (crypto/rand, не math/rand — секрет
// для аутентификации, не демо-данные), hex-encoded. Используется CLI
// `admin create-key`: raw показывается оператору РОВНО ОДИН РАЗ при
// создании, сохраняется только его hash.
func NewRawToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
