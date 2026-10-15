package domain

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

var testItemID = uuid.New()

type fakeGate struct{ blocked bool }

func (g *fakeGate) IsBlocked(ctx context.Context, itemID uuid.UUID) (bool, error) {
	return g.blocked, nil
}

type fakeGeocoder struct {
	err    error
	result GeoResult
	calls  int
}

func (g *fakeGeocoder) Resolve(ctx context.Context, address string) (GeoResult, error) {
	g.calls++
	if g.err != nil {
		return GeoResult{}, g.err
	}
	return g.result, nil
}

type fakeCache struct {
	get      map[string]GeoResult
	setCalls map[string]GeoResult
}

func newFakeCache() *fakeCache {
	return &fakeCache{get: map[string]GeoResult{}, setCalls: map[string]GeoResult{}}
}

func (c *fakeCache) Get(ctx context.Context, addr string) (GeoResult, bool, error) {
	r, ok := c.get[addr]
	return r, ok, nil
}

func (c *fakeCache) Set(ctx context.Context, addr string, r GeoResult) error {
	c.setCalls[addr] = r
	return nil
}

type fakeGeoWriter struct {
	saved  *GeoResult
	itemID uuid.UUID
}

func (w *fakeGeoWriter) SaveGeo(ctx context.Context, itemID uuid.UUID, r GeoResult) error {
	cp := r
	w.saved = &cp
	w.itemID = itemID
	return nil
}

type fakeEventPublisher struct {
	events []struct {
		itemID                uuid.UUID
		stage, status, errMsg string
	}
}

func (p *fakeEventPublisher) PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error {
	p.events = append(p.events, struct {
		itemID                uuid.UUID
		stage, status, errMsg string
	}{itemID, stage, status, errMsg})
	return nil
}

func (p *fakeEventPublisher) last() (stage, status string, ok bool) {
	if len(p.events) == 0 {
		return "", "", false
	}
	e := p.events[len(p.events)-1]
	return e.stage, e.status, true
}

func TestGeocodeItem_Blocked_SkipsEverything(t *testing.T) {
	geocoder := &fakeGeocoder{}
	writer := &fakeGeoWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{blocked: true}, geocoder, newFakeCache(), writer, events)

	if err := svc.GeocodeItem(context.Background(), testItemID, "Main St 1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if geocoder.calls != 0 {
		t.Errorf("geocoder вызван %d раз(а), want 0 — заблокированный Item не должен обрабатываться", geocoder.calls)
	}
	if writer.saved != nil {
		t.Error("writer.saved != nil, want nil")
	}
	if stage, status, ok := events.last(); !ok || stage != "geocode" || status != "blocked" {
		t.Errorf("последнее событие = (%q,%q), want (geocode,blocked)", stage, status)
	}
}

func TestGeocodeItem_EmptyAddress_EmitsNA(t *testing.T) {
	geocoder := &fakeGeocoder{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, geocoder, newFakeCache(), &fakeGeoWriter{}, events)

	if err := svc.GeocodeItem(context.Background(), testItemID, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if geocoder.calls != 0 {
		t.Errorf("geocoder вызван %d раз(а), want 0 — пустой адрес ⟹ n/a, не запрос наружу", geocoder.calls)
	}
	if stage, status, ok := events.last(); !ok || stage != "geocode" || status != "n/a" {
		t.Errorf("последнее событие = (%q,%q), want (geocode,n/a)", stage, status)
	}
}

func TestGeocodeItem_CacheHit_SkipsGeocoderCall(t *testing.T) {
	geocoder := &fakeGeocoder{result: GeoResult{Lat: 1, Lng: 2}} // не должен быть использован
	cache := newFakeCache()
	cache.get["Main St 1"] = GeoResult{Lat: 10, Lng: 20}
	writer := &fakeGeoWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, geocoder, cache, writer, events)

	if err := svc.GeocodeItem(context.Background(), testItemID, "Main St 1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if geocoder.calls != 0 {
		t.Errorf("geocoder вызван %d раз(а), want 0 — тот же адрес не должен бить во внешний API повторно (CLAUDE.md §3.4)", geocoder.calls)
	}
	if writer.saved == nil || writer.saved.Lat != 10 || writer.saved.Lng != 20 {
		t.Errorf("writer.saved=%+v, want закэшированный результат {10,20}, не из geocoder", writer.saved)
	}
	if stage, status, ok := events.last(); !ok || stage != "geocode" || status != "done" {
		t.Errorf("последнее событие = (%q,%q), want (geocode,done)", stage, status)
	}
}

// Регрессионный тест на конкретную находку этой сессии: cache-miss ОБЯЗАН
// реально резолвить через geocoder и сохранить результат — предыдущая
// версия молча публиковала "done" без единого вызова geocoder/writer.
func TestGeocodeItem_CacheMiss_ResolvesAndSavesAndCaches(t *testing.T) {
	geocoder := &fakeGeocoder{result: GeoResult{Lat: 5, Lng: 6}}
	cache := newFakeCache() // пусто — гарантированный miss
	writer := &fakeGeoWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, geocoder, cache, writer, events)

	if err := svc.GeocodeItem(context.Background(), testItemID, "New Address 1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if geocoder.calls != 1 {
		t.Fatalf("geocoder.calls=%d, want 1 — cache-miss обязан резолвить через geocoder", geocoder.calls)
	}
	if writer.saved == nil || writer.saved.Lat != 5 || writer.saved.Lng != 6 {
		t.Errorf("writer.saved=%+v, want результат от geocoder {5,6}", writer.saved)
	}
	if got, ok := cache.setCalls["New Address 1"]; !ok || got.Lat != 5 || got.Lng != 6 {
		t.Errorf("cache.setCalls[address]=%+v (ok=%v), want результат geocoder сохранённым в кеш", got, ok)
	}
	if stage, status, ok := events.last(); !ok || stage != "geocode" || status != "done" {
		t.Errorf("последнее событие = (%q,%q), want (geocode,done)", stage, status)
	}
}

func TestGeocodeItem_GeocoderError_StopsAndEmitsError(t *testing.T) {
	geocoder := &fakeGeocoder{err: errors.New("rate limited")}
	writer := &fakeGeoWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, geocoder, newFakeCache(), writer, events)

	err := svc.GeocodeItem(context.Background(), testItemID, "Some Address")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if writer.saved != nil {
		t.Error("writer.saved != nil — не должен писаться при ошибке geocoder")
	}
	if stage, status, ok := events.last(); !ok || stage != "geocode" || status != "error" {
		t.Errorf("последнее событие = (%q,%q), want (geocode,error)", stage, status)
	}
}
