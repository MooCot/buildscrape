package domain

import (
	"context"
	"errors"
	"sync"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

var ErrAllProvidersUnavailable = errors.New("all ai providers unavailable (circuit open or failing)")

type Service interface {
	ClassifyItem(ctx context.Context, itemID uuid.UUID, description string) error
}

type service struct {
	gate         shareddomain.ItemGateChecker // must — MODEL §7 P2, F5 входит в единый порт
	providers    []Provider                   // fallback между провайдерами, CLAUDE.md §3.5
	breakerStore BreakerStore                 // persistent state per provider, переживает рестарт
	writer       ItemAIWriter
	events       EventPublisher

	mu       sync.Mutex
	breakers map[string]*CircuitBreaker // лениво гидратируются из breakerStore, защищены mu
}

func NewService(g shareddomain.ItemGateChecker, providers []Provider, store BreakerStore, w ItemAIWriter, ev EventPublisher) Service {
	return &service{
		gate:         g,
		providers:    providers,
		breakerStore: store,
		writer:       w,
		events:       ev,
		breakers:     map[string]*CircuitBreaker{},
	}
}

// breakerFor — лениво создаёт/гидратирует breaker конкретного провайдера;
// concurrent-safe (несколько goroutine consumer'а могут вызывать
// ClassifyItem одновременно, §1.1 ТЕОРИЯ-УГЛУБЛЁННО.md).
func (s *service) breakerFor(ctx context.Context, name string) *CircuitBreaker {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cb, ok := s.breakers[name]; ok {
		return cb
	}
	snap, _ := s.breakerStore.Load(ctx, name) // best-effort: ошибка/не найдено → zero value → Closed
	cb := NewCircuitBreakerFromSnapshot(snap)
	s.breakers[name] = cb
	return cb
}

func (s *service) ClassifyItem(ctx context.Context, itemID uuid.UUID, description string) error {
	blocked, err := s.gate.IsBlocked(ctx, itemID)
	if err != nil {
		return err
	}
	if blocked {
		return s.events.PublishStageEvent(ctx, itemID, "ai", "blocked", "")
	}
	var lastErr error = ErrAllProvidersUnavailable
	for _, p := range s.providers {
		cb := s.breakerFor(ctx, p.Name)
		if !cb.Allow() {
			continue // ЭТОТ провайдер fail-fast — пробуем следующего в fallback-цепочке, не всю операцию
		}
		res, err := p.Classifier.Classify(ctx, description)
		if err == nil {
			cb.RecordSuccess()
			_ = s.breakerStore.Save(ctx, p.Name, cb.Snapshot())
			if err := s.writer.SaveClassification(ctx, itemID, res); err != nil {
				return err
			}
			return s.events.PublishStageEvent(ctx, itemID, "ai", "done", "")
		}
		cb.RecordFailure()
		_ = s.breakerStore.Save(ctx, p.Name, cb.Snapshot())
		lastErr = err
	}
	_ = s.events.PublishStageEvent(ctx, itemID, "ai", "error", lastErr.Error())
	return lastErr // все провайдеры отказали или были открыты — сообщение уходит на retry/DLQ
}
