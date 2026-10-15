// Package shareddomain — доменная логика, общая между стадиями, но НЕ
// константа/frozen-реестр (это internal/core) и НЕ инфра-клиент (это
// internal/platform). Единственное разрешённое исключение из правила
// "domain одной стадии не импортирует domain другой стадии напрямую"
// (см. CLAUDE.md §8, раздел "Правило зависимостей") — лежит ниже всех
// стадий по той же причине, что и internal/core.
//
// Названо shareddomain (не просто domain), чтобы Media/Geocode/AI/Storage
// могли импортировать этот пакет БЕЗ import-алиаса рядом со своим
// собственным internal/<stage>/domain (тоже называемым просто domain) —
// одинаковое имя пакета иначе потребовало бы алиасить один из двух на
// каждом сайте использования.
package shareddomain

import (
	"context"

	"github.com/google/uuid"
)

// ID — алиас на uuid.UUID (не отдельный тип: shareddomain.ID и uuid.UUID
// взаимозаменяемы без приведения). Раньше был `= string` — сознательная
// заглушка на время, пока скелет собирался без единой внешней зависимости
// (см. go.mod); теперь, когда github.com/google/uuid подключён, — настоящий
// тип. ExternalID (parse/domain.Item) остаётся string намеренно — это
// идентификатор ИЗ ИСТОЧНИКА (не обязательно UUID), не наш собственный.
type ID = uuid.UUID

// SourcePhase — производная фаза Source, см. MODEL-buildscrape.md §1.6
// (Existence × Archived × Enabled, Archived приоритетнее Enabled).
type SourcePhase string

const (
	SourcePhaseActive    SourcePhase = "active"
	SourcePhaseSuspended SourcePhase = "suspended"
	SourcePhaseArchived  SourcePhase = "archived"
)

// ItemGateChecker реализует C8 (MODEL-buildscrape.md §6) — единое
// определение, ⟺ (blocked_reason IS NOT NULL) ∨ (Source-фаза источника ≠
// active). Media/Geocode/AI/Storage вызывают ровно этот метод, не собирают
// условие каждый заново — решение 4.C, закрывает класс дублирования,
// найденный постфактум в wtgspain (два независимых WHERE в двух
// репозиториях) и случайно воспроизведённый для Storage/F6 в предыдущей
// редакции модели этого проекта (см. MODEL-buildscrape.md §5/§9).
type ItemGateChecker interface {
	IsBlocked(ctx context.Context, itemID ID) (bool, error)
}

// SourceRepository — единственный писатель всех граней Source (F7/F8/F9),
// единственный источник Source-фазы для чтения. Прямой читатель — только F1
// (Fetch, через Phase, до записи RawItem). F3/F4/F5/F6 фазу источника
// напрямую не читают вообще — только косвенно, внутри реализации
// ItemGateChecker.
type SourceRepository interface {
	Create(ctx context.Context, s Source) (Source, error)            // F7
	SetEnabled(ctx context.Context, sourceID ID, enabled bool) error // F8
	Archive(ctx context.Context, sourceID ID) error                  // F9 — одностороннее, нет archived→active
	Phase(ctx context.Context, sourceID ID) (SourcePhase, error)     // читает F1
}

// Source — доменное представление; поле Archived приоритетнее Enabled при
// вычислении Phase (C6, MODEL-buildscrape.md §6).
type Source struct {
	ID              ID
	Name            string
	BaseURL         string
	PollIntervalSec int
	Enabled         bool
	Archived        bool
}
