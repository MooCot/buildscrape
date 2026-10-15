package domain

import (
	"context"
	"fmt"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

type Service interface {
	ProcessItem(ctx context.Context, itemID uuid.UUID, photoURLs []string) error
}

type service struct {
	gate      shareddomain.ItemGateChecker // must-проверка перед действием (решение 4.C)
	processor ImageProcessor
	store     ObjectStore
	writer    MediaWriter
	events    EventPublisher
}

func NewService(g shareddomain.ItemGateChecker, p ImageProcessor, s ObjectStore, w MediaWriter, ev EventPublisher) Service {
	return &service{gate: g, processor: p, store: s, writer: w, events: ev}
}

var mediaKinds = []MediaKind{MediaThumbnail, MediaPreview, MediaFull}

func (s *service) ProcessItem(ctx context.Context, itemID uuid.UUID, photoURLs []string) error {
	blocked, err := s.gate.IsBlocked(ctx, itemID)
	if err != nil {
		return err
	}
	if blocked {
		return s.events.PublishStageEvent(ctx, itemID, "media", "blocked", "") // C3/C8
	}
	if len(photoURLs) == 0 {
		return s.events.PublishStageEvent(ctx, itemID, "media", "n/a", "") // Media-состояние n/a, MODEL §1.6
	}
	for i, url := range photoURLs {
		for _, kind := range mediaKinds {
			data, err := s.processor.Process(ctx, url, kind)
			if err != nil {
				_ = s.events.PublishStageEvent(ctx, itemID, "media", "error", err.Error())
				return err
			}
			// НАЙДЕНО при переходе на реальный SQL: ключ БЕЗ индекса URL
			// (itemID+"/"+kind) перезаписывал бы один и тот же S3-объект при
			// нескольких photoURL одного item — последняя фотография молча
			// затирала бы предыдущие в хранилище. Индекс i делает ключ
			// уникальным на (item, URL, kind).
			key := fmt.Sprintf("%s/%d-%s", itemID, i, kind)
			if err := s.store.Put(ctx, key, data); err != nil {
				_ = s.events.PublishStageEvent(ctx, itemID, "media", "error", err.Error())
				return err
			}
			// НАЙДЕНО при написании реального SQL: ID здесь раньше не
			// проставлялся вообще (zero-value uuid.UUID) — при item_media.id
			// PRIMARY KEY второй INSERT в рамках того же ProcessItem упал бы
			// на конфликте первичного ключа. Домен генерирует id сам (тот
			// же паттерн, что Fetch для RawItem.ID) — не полагается на
			// DEFAULT gen_random_uuid() в схеме.
			if err := s.writer.Save(ctx, ItemMedia{ID: uuid.New(), ItemID: itemID, S3Key: key, Kind: kind}); err != nil {
				return err
			}
		}
	}
	return s.events.PublishStageEvent(ctx, itemID, "media", "done", "")
}
