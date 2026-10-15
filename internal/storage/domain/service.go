package domain

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// ItemQueryService — F6. Читает ТОЛЬКО через ItemReader, который сам отдаёт
// лишь незаблокированные item'ы (гейт C8 — в реализации ридера, JOIN с
// sources; там же задокументировано единственное осознанное дублирование
// формулы ради bulk-запроса без N+1). Сервис не фильтрует повторно и не
// дёргает Phase напрямую — это была бы ещё одна копия гейта (MODEL-
// buildscrape.md §9, "исправлено после проверки на дублирование").
type ItemQueryService interface {
	List(ctx context.Context, filter ItemFilter) ([]ItemView, error)
	// Get — 404 (shareddomain.ErrNotFound) и для несуществующего, и для
	// заблокированного item: то, что скрыто из списка, не должно быть
	// доступно прямо по id (утечка мимо гейта).
	Get(ctx context.Context, id uuid.UUID) (ItemView, error)
}

type itemQueryService struct {
	reader ItemReader
}

func NewItemQueryService(r ItemReader) ItemQueryService {
	return &itemQueryService{reader: r}
}

func (s *itemQueryService) List(ctx context.Context, filter ItemFilter) ([]ItemView, error) {
	return s.reader.List(ctx, filter)
}

func (s *itemQueryService) Get(ctx context.Context, id uuid.UUID) (ItemView, error) {
	return s.reader.Get(ctx, id)
}

// ErrInvalidSource — создание источника отклонено валидацией (HTTP 400).
var ErrInvalidSource = errors.New("invalid source")

const (
	defaultPollIntervalSec = 300
	// minPollIntervalSec — нижняя граница: интервал в 1с превратил бы
	// планировщик в DoS чужого сайта (а на демо — в бесконечный поток 429).
	minPollIntervalSec = 5
)

// SourceAdminService — F7/F8/F9. Единственный писатель всех граней Source —
// напрямую через shareddomain.SourceRepository (не через ItemGateChecker,
// который только ЧИТАЕТ Phase косвенно).
type SourceAdminService interface {
	CreateSource(ctx context.Context, s shareddomain.Source) (shareddomain.Source, error) // F7
	SetEnabled(ctx context.Context, sourceID uuid.UUID, enabled bool) error               // F8
	Archive(ctx context.Context, sourceID uuid.UUID) error                                // F9 — одностороннее
}

type sourceAdminService struct {
	repo shareddomain.SourceRepository
}

func NewSourceAdminService(repo shareddomain.SourceRepository) SourceAdminService {
	return &sourceAdminService{repo: repo}
}

// CreateSource — домен владеет идентичностью (uuid.New() здесь, не DEFAULT
// в схеме — тот же принцип, что Fetch для RawItem.ID) и правилами: имя
// непусто, base_url — абсолютный http(s) URL, интервал ≥ 5с (0 — по
// умолчанию 300). Новый источник создаётся НЕ заархивированным: archived —
// одностороннее состояние, начинать жизнь с него бессмысленно.
func (s *sourceAdminService) CreateSource(ctx context.Context, src shareddomain.Source) (shareddomain.Source, error) {
	src.Name = strings.TrimSpace(src.Name)
	if src.Name == "" {
		return shareddomain.Source{}, fmt.Errorf("%w: name is required", ErrInvalidSource)
	}
	u, err := url.Parse(strings.TrimSpace(src.BaseURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return shareddomain.Source{}, fmt.Errorf("%w: base_url must be an absolute http(s) URL", ErrInvalidSource)
	}
	src.BaseURL = u.String()

	switch {
	case src.PollIntervalSec == 0:
		src.PollIntervalSec = defaultPollIntervalSec
	case src.PollIntervalSec < minPollIntervalSec:
		return shareddomain.Source{}, fmt.Errorf("%w: poll_interval_sec must be ≥ %d", ErrInvalidSource, minPollIntervalSec)
	}

	src.ID = uuid.New()
	src.Archived = false
	return s.repo.Create(ctx, src)
}

func (s *sourceAdminService) SetEnabled(ctx context.Context, sourceID uuid.UUID, enabled bool) error {
	return s.repo.SetEnabled(ctx, sourceID, enabled)
}

func (s *sourceAdminService) Archive(ctx context.Context, sourceID uuid.UUID) error {
	return s.repo.Archive(ctx, sourceID)
}
