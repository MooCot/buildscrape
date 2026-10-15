package domain

import (
	"context"
	"fmt"
	"strings"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

type Service interface {
	// candidateItemID — item_id, сгенерированный на Fetch (CLAUDE.md §4.4),
	// сквозной через весь pipeline; upsert обязан его сохранить при первой
	// вставке, но оставить существующий id при конфликте по (source_id,
	// external_id) — см. ItemWriter doc-comment.
	Parse(ctx context.Context, rawHTML string, sourceID, candidateItemID uuid.UUID, externalID string) error
}

type service struct {
	extractor Extractor
	writer    ItemWriter
	publisher NextStagePublisher
	events    EventPublisher
}

func NewService(ex Extractor, w ItemWriter, pub NextStagePublisher, ev EventPublisher) Service {
	return &service{extractor: ex, writer: w, publisher: pub, events: ev}
}

func (s *service) Parse(ctx context.Context, rawHTML string, sourceID, candidateItemID uuid.UUID, externalID string) error {
	ex, err := s.extractor.Extract(rawHTML)
	if err != nil {
		// Разбор детерминирован: та же страница даст ту же ошибку — повтор
		// бессмысленен, сразу в parse.dlq.
		return fmt.Errorf("%w: extract: %v", shareddomain.ErrPermanent, err)
	}
	// Заглушка/CAPTCHA — НЕ ошибка обработки, а состояние Item: он
	// сохраняется с blocked_reason (гейт A) и дальше по pipeline не идёт.
	// Валидируем поля только для настоящего контента.
	if ex.BlockedReason == nil {
		if err := validate(ex); err != nil {
			return fmt.Errorf("%w: %v", shareddomain.ErrPermanent, err)
		}
	}

	item, err := s.writer.Upsert(ctx, Item{
		ID:            candidateItemID,
		SourceID:      sourceID,
		ExternalID:    externalID,
		Title:         ex.Title,
		Description:   ex.Description,
		Price:         ex.Price,
		Address:       ex.Address,
		BlockedReason: ex.BlockedReason,
	})
	if err != nil {
		return err
	}
	return s.dispatch(ctx, item, ex.PhotoURLs)
}

// validate — обязательные поля (CLAUDE.md §3.2): без заголовка и положительной
// цены карточка не имеет смысла для выдачи. Невалидная запись не теряется
// молча — уходит в parse.dlq.
func validate(ex Extracted) error {
	var missing []string
	if strings.TrimSpace(ex.Title) == "" {
		missing = append(missing, "title")
	}
	if ex.Price <= 0 {
		missing = append(missing, "price")
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid listing: missing/empty %s", strings.Join(missing, ", "))
	}
	return nil
}

// dispatch — оркестрация hand-off F2→{F3,F4,F5} по уже сохранённому Item
// (с проставленным ID). Публикует три стадии независимо (Media/Geocode/AI
// — независимые параллельные писатели, MODEL-buildscrape.md §1.6), гейт A
// проверяется здесь же (заблокированный на Parse Item дальше не идёт вовсе
// — не "каждая стадия сама отфильтрует", экономия бессмысленной доставки).
func (s *service) dispatch(ctx context.Context, item Item, photoURLs []string) error {
	if item.BlockedReason != nil {
		return s.events.PublishStageEvent(ctx, item.ID, "parse", "blocked", "")
	}
	if err := s.publisher.PublishMediaJob(ctx, item.ID, photoURLs); err != nil {
		return err
	}
	if item.Address != "" {
		if err := s.publisher.PublishGeocodeJob(ctx, item.ID, item.Address); err != nil {
			return err
		}
	}
	if err := s.publisher.PublishAIJob(ctx, item.ID, item.Description); err != nil {
		return err
	}
	return s.events.PublishStageEvent(ctx, item.ID, "parse", "done", "")
}
