package domain

import (
	"context"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

type Service interface {
	GeocodeItem(ctx context.Context, itemID uuid.UUID, address string) error
}

type service struct {
	gate     shareddomain.ItemGateChecker
	geocoder Geocoder
	cache    AddressCache
	writer   ItemGeoWriter
	events   EventPublisher
}

func NewService(g shareddomain.ItemGateChecker, gc Geocoder, c AddressCache, w ItemGeoWriter, ev EventPublisher) Service {
	return &service{gate: g, geocoder: gc, cache: c, writer: w, events: ev}
}

func (s *service) GeocodeItem(ctx context.Context, itemID uuid.UUID, address string) error {
	blocked, err := s.gate.IsBlocked(ctx, itemID)
	if err != nil {
		return err
	}
	if blocked {
		return s.events.PublishStageEvent(ctx, itemID, "geocode", "blocked", "")
	}
	if address == "" {
		return s.events.PublishStageEvent(ctx, itemID, "geocode", "n/a", "") // Geocode-состояние n/a, MODEL §1.6
	}

	result, cached, err := s.cache.Get(ctx, address)
	if err != nil {
		return err
	}
	if !cached {
		// НАЙДЕНО при написании теста: предыдущая версия на cache-miss
		// публиковала "done", ни разу не вызвав ни geocoder.Resolve, ни
		// writer.SaveGeo — заглушка "TODO" молча выдавала себя за успех.
		result, err = s.geocoder.Resolve(ctx, address)
		if err != nil {
			_ = s.events.PublishStageEvent(ctx, itemID, "geocode", "error", err.Error())
			return err
		}
		if err := s.cache.Set(ctx, address, result); err != nil {
			return err
		}
	}
	if err := s.writer.SaveGeo(ctx, itemID, result); err != nil {
		return err
	}
	return s.events.PublishStageEvent(ctx, itemID, "geocode", "done", "")
}
