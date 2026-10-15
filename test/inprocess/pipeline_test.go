// Package inprocess — сквозные тесты pipeline БЕЗ внешней инфраструктуры:
// реальные компоненты (мок-источник по настоящему HTTP, HTTPPageFetcher,
// оба HTML-экстрактора, домены Fetch и Parse), но БД и очередь — fake в
// памяти. Отвечают на вопрос "цепочка вообще работает целиком?", на который
// юнит-тесты отдельных звеньев ответить не могут: каждое звено там
// проверяется против fake соседа, а не против настоящего.
//
// НЕ заменяют test/e2e (testcontainers, реальные Postgres/RabbitMQ) — SQL и
// AMQP здесь не участвуют вовсе.
package inprocess

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	fetchout "buildscrape/internal/fetch/adapter/outbound"
	fetchdomain "buildscrape/internal/fetch/domain"
	"buildscrape/internal/mocksource"
	parseout "buildscrape/internal/parse/adapter/outbound"
	parsedomain "buildscrape/internal/parse/domain"
	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// --- in-memory "инфраструктура" ---

type key struct {
	source uuid.UUID
	ext    string
}

// memStore эмулирует СЕМАНТИКУ SQL-upsert'ов (не сам SQL): на конфликте
// сохраняется существующий id, а блокировка не затирает контент. Сами SQL-
// запросы этим тестом не проверяются.
type memStore struct {
	mu    sync.Mutex
	raw   map[key]fetchdomain.RawItem
	items map[key]parsedomain.Item
}

func newMemStore() *memStore {
	return &memStore{raw: map[key]fetchdomain.RawItem{}, items: map[key]parsedomain.Item{}}
}

func (s *memStore) Save(ctx context.Context, r fetchdomain.RawItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{r.SourceID, r.ExternalID}
	if old, ok := s.raw[k]; ok {
		r.ID = old.ID
	}
	s.raw[k] = r
	return nil
}

func (s *memStore) Upsert(ctx context.Context, it parsedomain.Item) (parsedomain.Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{it.SourceID, it.ExternalID}
	if old, ok := s.items[k]; ok {
		it.ID = old.ID
		if it.BlockedReason != nil { // CASE ... ELSE items.title
			it.Title, it.Description, it.Price, it.Address = old.Title, old.Description, old.Price, old.Address
		}
	}
	s.items[k] = it
	return it, nil
}

type parseJob struct {
	rawItemID, sourceID uuid.UUID
	extID, html         string
}
type mediaJob struct {
	itemID uuid.UUID
	urls   []string
}
type textJob struct {
	itemID uuid.UUID
	text   string
}
type fetchJob struct {
	sourceID uuid.UUID
	url, ext string
}

// bus — все публикации pipeline в одном месте: реализует сразу порты
// публикации Fetch и Parse (имена методов не пересекаются).
type bus struct {
	mu       sync.Mutex
	fetch    []fetchJob
	parse    []parseJob
	media    []mediaJob
	geocode  []textJob
	ai       []textJob
	blocked  int
	doneEvts int
}

func (b *bus) PublishFetchJob(ctx context.Context, sourceID uuid.UUID, url, ext string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fetch = append(b.fetch, fetchJob{sourceID, url, ext})
	return nil
}
func (b *bus) PublishParseJob(ctx context.Context, rawID, sourceID uuid.UUID, ext, html string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.parse = append(b.parse, parseJob{rawID, sourceID, ext, html})
	return nil
}
func (b *bus) PublishMediaJob(ctx context.Context, id uuid.UUID, urls []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.media = append(b.media, mediaJob{id, urls})
	return nil
}
func (b *bus) PublishGeocodeJob(ctx context.Context, id uuid.UUID, addr string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.geocode = append(b.geocode, textJob{id, addr})
	return nil
}
func (b *bus) PublishAIJob(ctx context.Context, id uuid.UUID, desc string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ai = append(b.ai, textJob{id, desc})
	return nil
}
func (b *bus) PublishStageEvent(ctx context.Context, id uuid.UUID, stage, status, errMsg string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch status {
	case "blocked":
		b.blocked++
	case "done":
		b.doneEvts++
	}
	return nil
}

type staticScheduler struct{ sources []shareddomain.Source }

func (s staticScheduler) ClaimDue(ctx context.Context, now time.Time) ([]shareddomain.Source, error) {
	return s.sources, nil
}

type phases map[uuid.UUID]shareddomain.SourcePhase

func (p phases) Phase(ctx context.Context, id uuid.UUID) (shareddomain.SourcePhase, error) {
	if ph, ok := p[id]; ok {
		return ph, nil
	}
	return shareddomain.SourcePhaseActive, nil
}
func (phases) Create(ctx context.Context, s shareddomain.Source) (shareddomain.Source, error) {
	return s, nil
}
func (phases) SetEnabled(ctx context.Context, id uuid.UUID, e bool) error { return nil }
func (phases) Archive(ctx context.Context, id uuid.UUID) error            { return nil }

// --- сборка стенда ---

type rig struct {
	site     *httptest.Server
	requests *atomic.Int64
	src      shareddomain.Source
	bus      *bus
	store    *memStore
	phases   phases
	proxies  int
	fetch    fetchdomain.Service
	parse    parsedomain.Service
}

func newRig(t *testing.T, opts mocksource.Options, proxies int) *rig {
	t.Helper()
	opts.Sleep = func(time.Duration) {}

	var requests atomic.Int64
	inner := mocksource.New(opts).Handler()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(site.Close)

	r := &rig{
		site: site, requests: &requests, bus: &bus{}, store: newMemStore(), phases: phases{},
		src: shareddomain.Source{ID: uuid.New(), Name: "mock", BaseURL: site.URL + "/listings", PollIntervalSec: 60, Enabled: true},
	}
	r.fetch = fetchdomain.NewService(fetchdomain.Deps{
		Proxies:   fetchout.NewMockProxyProvider(proxies),
		Pages:     fetchout.NewHTTPPageFetcher(5 * time.Second),
		Index:     fetchout.HTMLIndexExtractor{},
		Scheduler: staticScheduler{[]shareddomain.Source{r.src}},
		Jobs:      r.bus,
		Writer:    r.store,
		Next:      r.bus,
		Sources:   r.phases,
	})
	r.parse = parsedomain.NewService(parseout.HTMLExtractor{}, r.store, r.bus, r.bus)
	return r
}

// drain — прогон pipeline по очередям "вручную": каждое задание из bus
// обрабатывается соответствующим сервисом, как это делали бы consumer'ы.
// Возвращает ошибки FetchOne/Parse (в норме — пусто).
func (r *rig) drain(t *testing.T) (fetchErrs, parseErrs []error) {
	t.Helper()
	ctx := context.Background()
	if err := r.fetch.ScheduleDue(ctx); err != nil {
		t.Fatalf("ScheduleDue: %v", err)
	}
	for _, j := range r.bus.fetch {
		if err := r.fetch.FetchOne(ctx, j.sourceID, j.url, j.ext); err != nil {
			fetchErrs = append(fetchErrs, err)
		}
	}
	for _, j := range r.bus.parse {
		if err := r.parse.Parse(ctx, j.html, j.sourceID, j.rawItemID, j.extID); err != nil {
			parseErrs = append(parseErrs, err)
		}
	}
	return
}

// --- сценарии ---

func TestPipeline_HappyPath_EveryListingBecomesItemAndFansOut(t *testing.T) {
	const n = 60 // 2 страницы индекса по 50 → пагинация тоже участвует
	r := newRig(t, mocksource.Options{Count: n, PageSize: 40}, 3)

	fErrs, pErrs := r.drain(t)
	if len(fErrs)+len(pErrs) != 0 {
		t.Fatalf("ошибки при нулевом хаосе: fetch=%v parse=%v", fErrs, pErrs)
	}

	if len(r.bus.fetch) != n || len(r.store.raw) != n || len(r.store.items) != n {
		t.Fatalf("fetchJobs=%d raw=%d items=%d, want %d/%d/%d", len(r.bus.fetch), len(r.store.raw), len(r.store.items), n, n, n)
	}

	var withAddr, withPhotos int
	for id := 1; id <= n; id++ {
		want := mocksource.Generate(id)
		it, ok := r.store.items[key{r.src.ID, strconv.Itoa(id)}]
		if !ok {
			t.Fatalf("id=%d: item не создан", id)
		}
		if it.Title != want.Title || it.Price != float64(want.Price) || it.Address != want.Address || it.Description != want.Description {
			t.Fatalf("id=%d: item=%+v, want данные %+v — данные исказились где-то в цепочке", id, it, want)
		}
		if want.Address != "" {
			withAddr++
		}
		if len(want.PhotoNames) > 0 {
			withPhotos++
		}
	}

	// Сквозной item_id: Item.ID — это RawItem.ID, рождённый на Fetch (§4.4).
	for k, it := range r.store.items {
		if it.ID != r.store.raw[k].ID {
			t.Fatalf("%v: Item.ID=%s ≠ RawItem.ID=%s — сквозного ключа нет", k, it.ID, r.store.raw[k].ID)
		}
	}

	// Fan-out: Media — безусловно (n/a решает сама Media), Geocode — только с
	// адресом, AI — всегда; и все три ссылаются на ТОТ ЖЕ id.
	if len(r.bus.media) != n || len(r.bus.ai) != n || len(r.bus.geocode) != withAddr {
		t.Errorf("media=%d ai=%d geocode=%d, want %d/%d/%d", len(r.bus.media), len(r.bus.ai), len(r.bus.geocode), n, n, withAddr)
	}
	gotPhotos := 0
	for _, m := range r.bus.media {
		if len(m.urls) > 0 {
			gotPhotos++
		}
	}
	if gotPhotos != withPhotos {
		t.Errorf("MediaJob с фото=%d, want %d — photo-ссылки должны дойти до Media", gotPhotos, withPhotos)
	}
	if r.bus.doneEvts != n {
		t.Errorf("событий parse/done=%d, want %d", r.bus.doneEvts, n)
	}
}

func TestPipeline_RepeatedPollIsIdempotentAndKeepsItemIDs(t *testing.T) {
	r := newRig(t, mocksource.Options{Count: 20}, 2)
	r.drain(t)
	before := map[key]uuid.UUID{}
	for k, it := range r.store.items {
		before[k] = it.ID
	}

	// Второй опрос: источник снова созрел, те же объявления.
	r.bus.fetch, r.bus.parse = nil, nil
	if fe, pe := r.drain(t); len(fe)+len(pe) != 0 {
		t.Fatalf("errors: %v %v", fe, pe)
	}

	if len(r.store.items) != 20 || len(r.store.raw) != 20 {
		t.Errorf("items=%d raw=%d, want 20/20 — повторный опрос не должен плодить дубликаты", len(r.store.items), len(r.store.raw))
	}
	for k, id := range before {
		if r.store.items[k].ID != id {
			t.Fatalf("%v: Item.ID сменился %s → %s между опросами", k, id, r.store.items[k].ID)
		}
	}
}

func TestPipeline_CaptchaStubs_BecomeBlockedItemsAndStopThePipeline(t *testing.T) {
	r := newRig(t, mocksource.Options{Count: 15, CaptchaProb: 1}, 2)

	if fe, pe := r.drain(t); len(fe)+len(pe) != 0 {
		t.Fatalf("заглушка не ошибка обработки: fetch=%v parse=%v", fe, pe)
	}
	if len(r.store.items) != 15 {
		t.Fatalf("items=%d, want 15 — заблокированные Item тоже сохраняются (гейт A)", len(r.store.items))
	}
	for k, it := range r.store.items {
		if it.BlockedReason == nil {
			t.Fatalf("%v: не помечен blocked", k)
		}
	}
	if len(r.bus.media)+len(r.bus.geocode)+len(r.bus.ai) != 0 {
		t.Error("заблокированный Item ушёл дальше по pipeline")
	}
	if r.bus.blocked != 15 {
		t.Errorf("событий parse/blocked=%d, want 15", r.bus.blocked)
	}
}

func TestPipeline_BlockedRefetchDoesNotWipeGoodContent(t *testing.T) {
	// Первый опрос — хороший контент; второй — источник отдаёт заглушки.
	good := newRig(t, mocksource.Options{Count: 5}, 2)
	good.drain(t)
	k := key{good.src.ID, "1"}
	orig := good.store.items[k]

	stub := mocksource.New(mocksource.Options{Count: 5, CaptchaProb: 1, Sleep: func(time.Duration) {}}).Handler()
	ts := httptest.NewServer(stub)
	defer ts.Close()

	ctx := context.Background()
	page, err := fetchout.NewHTTPPageFetcher(5*time.Second).Get(ctx, fetchdomain.Proxy{}, ts.URL+"/listings/1")
	if err != nil {
		t.Fatal(err)
	}
	if err := good.parse.Parse(ctx, page.Body, good.src.ID, uuid.New(), "1"); err != nil {
		t.Fatal(err)
	}

	got := good.store.items[k]
	if got.BlockedReason == nil {
		t.Error("Item должен стать blocked")
	}
	if got.Title != orig.Title || got.Price != orig.Price || got.Description != orig.Description {
		t.Errorf("заглушка затёрла хороший контент: было %+v, стало %+v", orig, got)
	}
	if got.ID != orig.ID {
		t.Error("Item.ID сменился")
	}
}

func TestPipeline_SourceNotActive_NoHTTPRequestsAtAll(t *testing.T) {
	r := newRig(t, mocksource.Options{Count: 10}, 2)
	r.phases[r.src.ID] = shareddomain.SourcePhaseSuspended

	if err := r.fetch.ScheduleDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := r.requests.Load(); n != 0 || len(r.bus.fetch) != 0 {
		t.Errorf("requests=%d fetchJobs=%d — выключенный источник не должен опрашиваться вовсе (P1)", n, len(r.bus.fetch))
	}
}

func TestPipeline_RateLimitedSource_RotatesProxiesThenRunsOutOfThem(t *testing.T) {
	r := newRig(t, mocksource.Options{Count: 5, RateLimitProb: 1}, 2)
	ctx := context.Background()
	url := r.site.URL + "/listings/1"

	// Каждый 429 банит прокси, выдавший его; два прокси → две попытки.
	for i := 1; i <= 2; i++ {
		err := r.fetch.FetchOne(ctx, r.src.ID, url, "1")
		if err == nil || shareddomain.IsPermanent(err) {
			t.Fatalf("попытка %d: err=%v, want временную (429 лечится ожиданием)", i, err)
		}
	}
	err := r.fetch.FetchOne(ctx, r.src.ID, url, "1")
	if err == nil || shareddomain.IsPermanent(err) {
		t.Fatalf("err=%v, want временную ошибку 'нет живых прокси'", err)
	}
	if got := r.requests.Load(); got != 2 {
		t.Errorf("запросов к источнику=%d, want 2 — с исчерпанным пулом запроса быть не должно", got)
	}
	if len(r.store.raw) != 0 || len(r.bus.parse) != 0 {
		t.Error("на 429 ничего не должно сохраняться и уходить в Parse")
	}
}

func TestPipeline_ListingGone_IsDroppedWithoutError(t *testing.T) {
	r := newRig(t, mocksource.Options{Count: 5}, 2)
	if err := r.fetch.FetchOne(context.Background(), r.src.ID, r.site.URL+"/listings/9999", "9999"); err != nil {
		t.Errorf("err=%v — 404 это 'объявление сняли', не ошибка", err)
	}
	if len(r.store.raw) != 0 {
		t.Error("ничего не должно сохраняться")
	}
}
