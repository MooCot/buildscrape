package domain

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// --- fakes ---

type fakeProxies struct {
	acquireErr error
	banned     []time.Duration
	bannedN    int
}

func (p *fakeProxies) Acquire(ctx context.Context) (Proxy, error) {
	if p.acquireErr != nil {
		return Proxy{}, p.acquireErr
	}
	return Proxy{Endpoint: "p1"}, nil
}

func (p *fakeProxies) MarkDead(ctx context.Context, _ Proxy, d time.Duration) {
	p.bannedN++
	p.banned = append(p.banned, d)
}

type fakePages struct {
	pages map[string]Page
	errs  map[string]error
	calls []string
}

func (f *fakePages) Get(ctx context.Context, _ Proxy, url string) (Page, error) {
	f.calls = append(f.calls, url)
	if err := f.errs[url]; err != nil {
		return Page{}, err
	}
	if p, ok := f.pages[url]; ok {
		return p, nil
	}
	return Page{Status: 404}, nil
}

type fakeIndex struct {
	byURL map[string]IndexPage
	err   error
}

func (f *fakeIndex) ExtractIndex(html, pageURL string) (IndexPage, error) {
	if f.err != nil {
		return IndexPage{}, f.err
	}
	return f.byURL[pageURL], nil
}

type fakeScheduler struct {
	due []shareddomain.Source
	err error
}

func (f *fakeScheduler) ClaimDue(ctx context.Context, now time.Time) ([]shareddomain.Source, error) {
	return f.due, f.err
}

type publishedJob struct {
	sourceID   uuid.UUID
	url, extID string
}

type fakeJobs struct {
	jobs []publishedJob
	err  error
}

func (f *fakeJobs) PublishFetchJob(ctx context.Context, sourceID uuid.UUID, url, externalID string) error {
	if f.err != nil {
		return f.err
	}
	f.jobs = append(f.jobs, publishedJob{sourceID, url, externalID})
	return nil
}

type fakeWriter struct {
	saved []RawItem
	err   error
}

func (w *fakeWriter) Save(ctx context.Context, item RawItem) error {
	if w.err != nil {
		return w.err
	}
	w.saved = append(w.saved, item)
	return nil
}

type parseJob struct {
	rawItemID, sourceID uuid.UUID
	externalID, html    string
}

type fakeNext struct {
	jobs []parseJob
	err  error
}

func (f *fakeNext) PublishParseJob(ctx context.Context, rawItemID, sourceID uuid.UUID, externalID, rawHTML string) error {
	if f.err != nil {
		return f.err
	}
	f.jobs = append(f.jobs, parseJob{rawItemID, sourceID, externalID, rawHTML})
	return nil
}

type fakeSources struct {
	phases map[uuid.UUID]shareddomain.SourcePhase // нет записи → active
}

func (f *fakeSources) Phase(ctx context.Context, id uuid.UUID) (shareddomain.SourcePhase, error) {
	if p, ok := f.phases[id]; ok {
		return p, nil
	}
	return shareddomain.SourcePhaseActive, nil
}
func (f *fakeSources) Create(ctx context.Context, s shareddomain.Source) (shareddomain.Source, error) {
	return s, nil
}
func (f *fakeSources) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) error { return nil }
func (f *fakeSources) Archive(ctx context.Context, id uuid.UUID) error                  { return nil }

type harness struct {
	svc     *service
	proxies *fakeProxies
	pages   *fakePages
	index   *fakeIndex
	sched   *fakeScheduler
	jobs    *fakeJobs
	writer  *fakeWriter
	next    *fakeNext
	sources *fakeSources
}

var fixedNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

func newHarness() *harness {
	h := &harness{
		proxies: &fakeProxies{},
		pages:   &fakePages{pages: map[string]Page{}, errs: map[string]error{}},
		index:   &fakeIndex{byURL: map[string]IndexPage{}},
		sched:   &fakeScheduler{},
		jobs:    &fakeJobs{},
		writer:  &fakeWriter{},
		next:    &fakeNext{},
		sources: &fakeSources{phases: map[uuid.UUID]shareddomain.SourcePhase{}},
	}
	h.svc = NewService(Deps{
		Proxies: h.proxies, Pages: h.pages, Index: h.index, Scheduler: h.sched,
		Jobs: h.jobs, Writer: h.writer, Next: h.next, Sources: h.sources,
	}).(*service)
	h.svc.now = func() time.Time { return fixedNow }
	return h
}

func source(name, base string) shareddomain.Source {
	return shareddomain.Source{ID: uuid.New(), Name: name, BaseURL: base, PollIntervalSec: 60, Enabled: true}
}

// --- ScheduleDue ---

func TestScheduleDue_PublishesJobPerListing(t *testing.T) {
	h := newHarness()
	src := source("mock", "http://src/listings")
	h.sched.due = []shareddomain.Source{src}
	h.pages.pages[src.BaseURL] = Page{Status: 200, Body: "<index/>"}
	h.index.byURL[src.BaseURL] = IndexPage{Listings: []Listing{
		{URL: "http://src/listings/1", ExternalID: "1"},
		{URL: "http://src/listings/2", ExternalID: "2"},
	}}

	if err := h.svc.ScheduleDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.jobs.jobs) != 2 {
		t.Fatalf("jobs=%d, want 2", len(h.jobs.jobs))
	}
	if j := h.jobs.jobs[0]; j.sourceID != src.ID || j.url != "http://src/listings/1" || j.extID != "1" {
		t.Errorf("job[0]=%+v — поля должны дойти без искажений", j)
	}
}

func TestScheduleDue_SkipsNonActiveSource(t *testing.T) {
	// P1: фаза консультируется ДО запроса — у выключенного источника индекс
	// не должен даже скачиваться.
	for _, phase := range []shareddomain.SourcePhase{shareddomain.SourcePhaseSuspended, shareddomain.SourcePhaseArchived} {
		h := newHarness()
		src := source("off", "http://src/listings")
		h.sched.due = []shareddomain.Source{src}
		h.sources.phases[src.ID] = phase

		if err := h.svc.ScheduleDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(h.pages.calls) != 0 || len(h.jobs.jobs) != 0 {
			t.Errorf("phase=%s: calls=%v jobs=%d, want ничего", phase, h.pages.calls, len(h.jobs.jobs))
		}
	}
}

func TestScheduleDue_FollowsPaginationAndDedups(t *testing.T) {
	h := newHarness()
	src := source("mock", "http://src/p1")
	h.sched.due = []shareddomain.Source{src}
	for _, u := range []string{"http://src/p1", "http://src/p2"} {
		h.pages.pages[u] = Page{Status: 200}
	}
	h.index.byURL["http://src/p1"] = IndexPage{
		Listings: []Listing{{URL: "http://src/l/1", ExternalID: "1"}, {URL: "http://src/l/2", ExternalID: "2"}},
		Next:     "http://src/p2",
	}
	h.index.byURL["http://src/p2"] = IndexPage{
		// l/2 повторяется на второй странице (сдвиг выдачи между запросами)
		Listings: []Listing{{URL: "http://src/l/2", ExternalID: "2"}, {URL: "http://src/l/3", ExternalID: "3"}},
	}

	if err := h.svc.ScheduleDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.jobs.jobs) != 3 {
		t.Errorf("jobs=%d, want 3 — повтор объявления на соседней странице не должен давать второе задание", len(h.jobs.jobs))
	}
}

func TestScheduleDue_CircularNextIsBounded(t *testing.T) {
	h := newHarness()
	src := source("loop", "http://src/p1")
	h.sched.due = []shareddomain.Source{src}
	h.pages.pages["http://src/p1"] = Page{Status: 200}
	h.index.byURL["http://src/p1"] = IndexPage{Next: "http://src/p1"} // "дальше" ведёт на себя
	h.svc.maxIndexPages = 5

	if err := h.svc.ScheduleDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(h.pages.calls) != 5 {
		t.Errorf("запросов индекса=%d, want ровно 5 (предохранитель), а не бесконечность", len(h.pages.calls))
	}
}

func TestScheduleDue_OneBrokenSourceDoesNotStarveOthers(t *testing.T) {
	h := newHarness()
	bad := source("bad", "http://bad/listings")
	good := source("good", "http://good/listings")
	h.sched.due = []shareddomain.Source{bad, good}
	h.pages.errs[bad.BaseURL] = errors.New("connection refused")
	h.pages.pages[good.BaseURL] = Page{Status: 200}
	h.index.byURL[good.BaseURL] = IndexPage{Listings: []Listing{{URL: "http://good/l/1", ExternalID: "1"}}}

	err := h.svc.ScheduleDue(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Errorf("err=%v, want ошибку с именем сломанного источника", err)
	}
	if len(h.jobs.jobs) != 1 || h.jobs.jobs[0].sourceID != good.ID {
		t.Errorf("jobs=%+v, want задание исправного источника несмотря на сбой соседа", h.jobs.jobs)
	}
}

func TestScheduleDue_ClaimErrorAbortsTick(t *testing.T) {
	h := newHarness()
	h.sched.err = errors.New("db down")
	if err := h.svc.ScheduleDue(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if len(h.pages.calls) != 0 {
		t.Error("без ClaimDue опрашивать нечего")
	}
}

func TestScheduleDue_UnparseableIndexIsPermanent(t *testing.T) {
	h := newHarness()
	src := source("weird", "http://src/listings")
	h.sched.due = []shareddomain.Source{src}
	h.pages.pages[src.BaseURL] = Page{Status: 200}
	h.index.err = errors.New("no listing links")

	err := h.svc.ScheduleDue(context.Background())
	if !shareddomain.IsPermanent(err) {
		t.Errorf("err=%v, want permanent — нераспознаваемый индекс повтором не починить", err)
	}
}

// --- FetchOne ---

func TestFetchOne_SavesRawItemAndHandsOffToParse(t *testing.T) {
	h := newHarness()
	sid := uuid.New()
	h.pages.pages["http://src/l/7"] = Page{Status: 200, Body: "<html>listing</html>"}

	if err := h.svc.FetchOne(context.Background(), sid, "http://src/l/7", "7"); err != nil {
		t.Fatal(err)
	}
	if len(h.writer.saved) != 1 || len(h.next.jobs) != 1 {
		t.Fatalf("saved=%d parseJobs=%d, want 1/1", len(h.writer.saved), len(h.next.jobs))
	}
	raw, job := h.writer.saved[0], h.next.jobs[0]
	if raw.SourceID != sid || raw.ExternalID != "7" || raw.RawHTML != "<html>listing</html>" || raw.Status != RawItemFetched || !raw.FetchedAt.Equal(fixedNow) {
		t.Errorf("raw=%+v", raw)
	}
	if raw.ID == uuid.Nil {
		t.Error("RawItem.ID не сгенерирован — item_id должен родиться на Fetch (CLAUDE.md §4.4)")
	}
	// Один и тот же id в записи и в hand-off — иначе сквозного ключа нет.
	if job.rawItemID != raw.ID || job.sourceID != sid || job.externalID != "7" || job.html != raw.RawHTML {
		t.Errorf("parse job %+v не соответствует сохранённому %+v", job, raw)
	}
}

func TestFetchOne_SaveFailureMeansNoHandOff(t *testing.T) {
	h := newHarness()
	h.pages.pages["http://src/l/1"] = Page{Status: 200, Body: "x"}
	h.writer.err = errors.New("db down")

	if err := h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1"); err == nil {
		t.Fatal("want error")
	}
	if len(h.next.jobs) != 0 {
		t.Error("Parse получил job на RawItem, которого нет в БД")
	}
}

func TestFetchOne_RateLimited_BansProxyForRetryAfterAndIsTransient(t *testing.T) {
	h := newHarness()
	h.pages.pages["http://src/l/1"] = Page{Status: 429, RetryAfter: 7 * time.Second}

	err := h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1")
	if err == nil || shareddomain.IsPermanent(err) {
		t.Fatalf("err=%v, want временную ошибку (→ backoff)", err)
	}
	if len(h.proxies.banned) != 1 || h.proxies.banned[0] != 7*time.Second {
		t.Errorf("banned=%v, want [7s] — срок из Retry-After источника", h.proxies.banned)
	}
	if len(h.writer.saved) != 0 {
		t.Error("ничего не должно сохраняться на 429")
	}
}

func TestFetchOne_RateLimitedWithoutRetryAfterUsesDefaultBan(t *testing.T) {
	h := newHarness()
	h.pages.pages["http://src/l/1"] = Page{Status: 429}
	_ = h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1")
	if len(h.proxies.banned) != 1 || h.proxies.banned[0] != defaultProxyBan {
		t.Errorf("banned=%v, want [%v]", h.proxies.banned, defaultProxyBan)
	}
}

func TestFetchOne_ServerError_IsTransientAndDoesNotBlameProxy(t *testing.T) {
	h := newHarness()
	h.pages.pages["http://src/l/1"] = Page{Status: 503}

	err := h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1")
	if err == nil || shareddomain.IsPermanent(err) {
		t.Fatalf("err=%v, want временную", err)
	}
	if h.proxies.bannedN != 0 {
		t.Error("падает источник, а не прокси — прокси банить не за что")
	}
}

func TestFetchOne_TransportError_BansProxyAndIsTransient(t *testing.T) {
	h := newHarness()
	h.pages.errs["http://src/l/1"] = errors.New("dial tcp: connection refused")

	err := h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1")
	if err == nil || shareddomain.IsPermanent(err) {
		t.Fatalf("err=%v, want временную", err)
	}
	if h.proxies.bannedN != 1 {
		t.Errorf("bannedN=%d, want 1 — сеть упала, подозреваем прокси", h.proxies.bannedN)
	}
}

func TestFetchOne_GoneIsDroppedQuietly(t *testing.T) {
	for _, status := range []int{404, 410} {
		h := newHarness()
		h.pages.pages["http://src/l/1"] = Page{Status: status}

		if err := h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1"); err != nil {
			t.Errorf("status=%d: err=%v, want nil — объявление сняли, это не ошибка", status, err)
		}
		if len(h.writer.saved) != 0 || len(h.next.jobs) != 0 {
			t.Errorf("status=%d: ничего не должно сохраняться/публиковаться", status)
		}
	}
}

func TestFetchOne_OtherClientErrorsArePermanent(t *testing.T) {
	h := newHarness()
	h.pages.pages["http://src/l/1"] = Page{Status: 403}

	err := h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1")
	if !shareddomain.IsPermanent(err) {
		t.Errorf("err=%v, want permanent — 403 повтором не лечится, в DLQ сразу", err)
	}
}

func TestFetchOne_InactiveSourceDropsJobWithoutFetching(t *testing.T) {
	h := newHarness()
	sid := uuid.New()
	h.sources.phases[sid] = shareddomain.SourcePhaseSuspended

	if err := h.svc.FetchOne(context.Background(), sid, "http://src/l/1", "1"); err != nil {
		t.Fatalf("err=%v, want nil — задание снимается, не ретраится", err)
	}
	if len(h.pages.calls) != 0 {
		t.Error("страница не должна скачиваться для выключенного источника")
	}
}

func TestFetchOne_NoLiveProxyIsTransient(t *testing.T) {
	h := newHarness()
	h.proxies.acquireErr = errors.New("no live proxy")

	err := h.svc.FetchOne(context.Background(), uuid.New(), "http://src/l/1", "1")
	if err == nil || shareddomain.IsPermanent(err) {
		t.Errorf("err=%v, want временную — прокси оживут через срок бана", err)
	}
	if len(h.pages.calls) != 0 {
		t.Error("без прокси запроса быть не должно")
	}
}
