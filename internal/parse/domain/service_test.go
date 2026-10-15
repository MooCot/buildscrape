package domain

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// --- fakes ---

type fakeExtractor struct {
	out Extracted
	err error
}

func (e *fakeExtractor) Extract(rawHTML string) (Extracted, error) { return e.out, e.err }

// fakeItemWriter — по умолчанию эхо входного item; override != nil имитирует
// БД, вернувшую ДРУГОЙ id (существующий при конфликте по source_id+external_id).
type fakeItemWriter struct {
	upserts    []Item
	overrideID *uuid.UUID
	err        error
}

func (w *fakeItemWriter) Upsert(ctx context.Context, item Item) (Item, error) {
	if w.err != nil {
		return Item{}, w.err
	}
	w.upserts = append(w.upserts, item)
	if w.overrideID != nil {
		item.ID = *w.overrideID
	}
	return item, nil
}

type mediaCall struct {
	itemID uuid.UUID
	urls   []string
}

type fakePublisher struct {
	mediaCalls   []mediaCall
	geocodeCalls []struct {
		itemID  uuid.UUID
		address string
	}
	aiCalls []struct {
		itemID      uuid.UUID
		description string
	}
	err error
}

func (p *fakePublisher) PublishMediaJob(ctx context.Context, itemID uuid.UUID, photoURLs []string) error {
	if p.err != nil {
		return p.err
	}
	p.mediaCalls = append(p.mediaCalls, mediaCall{itemID, photoURLs})
	return nil
}

func (p *fakePublisher) PublishGeocodeJob(ctx context.Context, itemID uuid.UUID, address string) error {
	p.geocodeCalls = append(p.geocodeCalls, struct {
		itemID  uuid.UUID
		address string
	}{itemID, address})
	return nil
}

func (p *fakePublisher) PublishAIJob(ctx context.Context, itemID uuid.UUID, description string) error {
	p.aiCalls = append(p.aiCalls, struct {
		itemID      uuid.UUID
		description string
	}{itemID, description})
	return nil
}

type fakeEventPublisher struct {
	events []struct{ itemID, stage, status string }
}

func (p *fakeEventPublisher) PublishStageEvent(ctx context.Context, itemID uuid.UUID, stage, status, errMsg string) error {
	p.events = append(p.events, struct{ itemID, stage, status string }{itemID.String(), stage, status})
	return nil
}

func (p *fakeEventPublisher) last() (stage, status string, ok bool) {
	if len(p.events) == 0 {
		return "", "", false
	}
	e := p.events[len(p.events)-1]
	return e.stage, e.status, true
}

var (
	testSourceID = uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	testItemID   = uuid.MustParse("00000000-0000-0000-0000-0000000000b1")
)

type harness struct {
	ex     *fakeExtractor
	writer *fakeItemWriter
	pub    *fakePublisher
	events *fakeEventPublisher
	svc    Service
}

func newHarness(out Extracted) *harness {
	h := &harness{ex: &fakeExtractor{out: out}, writer: &fakeItemWriter{}, pub: &fakePublisher{}, events: &fakeEventPublisher{}}
	h.svc = NewService(h.ex, h.writer, h.pub, h.events)
	return h
}

func (h *harness) parse() error {
	return h.svc.Parse(context.Background(), "<html/>", testSourceID, testItemID, "ext-1")
}

func validContent() Extracted {
	return Extracted{
		Title: "Кирпичный дом, 120 м²", Price: 1250000, Address: "г. Киев, ул. Садовая, 5",
		Description: "Участок ровный.", PhotoURLs: []string{"http://s/photos/1-1.png", "http://s/photos/1-2.png"},
	}
}

// --- тесты ---

func TestParse_ValidListing_UpsertsExtractedFieldsAndPublishesAllThree(t *testing.T) {
	h := newHarness(validContent())
	if err := h.parse(); err != nil {
		t.Fatal(err)
	}

	if len(h.writer.upserts) != 1 {
		t.Fatalf("upserts=%d, want 1", len(h.writer.upserts))
	}
	got := h.writer.upserts[0]
	want := Item{
		ID: testItemID, SourceID: testSourceID, ExternalID: "ext-1",
		Title: "Кирпичный дом, 120 м²", Description: "Участок ровный.", Price: 1250000, Address: "г. Киев, ул. Садовая, 5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("upsert item=%+v, want %+v — извлечённые поля и идентичность должны дойти до записи как есть", got, want)
	}
	if len(h.pub.mediaCalls) != 1 || len(h.pub.geocodeCalls) != 1 || len(h.pub.aiCalls) != 1 {
		t.Errorf("публикации media/geocode/ai = %d/%d/%d, want 1/1/1", len(h.pub.mediaCalls), len(h.pub.geocodeCalls), len(h.pub.aiCalls))
	}
	if stage, status, ok := h.events.last(); !ok || stage != "parse" || status != "done" {
		t.Errorf("последнее событие = (%q,%q), want (parse,done)", stage, status)
	}
}

// Раньше PublishMediaJob получал nil: photo-ссылки нигде не извлекались.
func TestParse_PhotoURLsReachMediaJob(t *testing.T) {
	c := validContent()
	h := newHarness(c)
	if err := h.parse(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(h.pub.mediaCalls[0].urls, c.PhotoURLs) {
		t.Errorf("media urls=%v, want %v", h.pub.mediaCalls[0].urls, c.PhotoURLs)
	}
}

func TestParse_DescriptionAndAddressReachTheirJobs(t *testing.T) {
	h := newHarness(validContent())
	_ = h.parse()
	if h.pub.geocodeCalls[0].address != "г. Киев, ул. Садовая, 5" {
		t.Errorf("geocode address=%q", h.pub.geocodeCalls[0].address)
	}
	if h.pub.aiCalls[0].description != "Участок ровный." {
		t.Errorf("ai description=%q", h.pub.aiCalls[0].description)
	}
}

func TestParse_EmptyAddress_SkipsOnlyGeocode(t *testing.T) {
	c := validContent()
	c.Address = ""
	h := newHarness(c)
	if err := h.parse(); err != nil {
		t.Fatal(err)
	}
	if len(h.pub.geocodeCalls) != 0 {
		t.Errorf("geocodeCalls=%d, want 0 — пустой адрес ⟹ GeocodeJob не публикуется (n/a)", len(h.pub.geocodeCalls))
	}
	if len(h.pub.mediaCalls) != 1 || len(h.pub.aiCalls) != 1 {
		t.Errorf("media/ai = %d/%d, want 1/1 — AI n/a не бывает, Media решает n/a по photoURLs сама", len(h.pub.mediaCalls), len(h.pub.aiCalls))
	}
}

func TestParse_NoPhotos_StillPublishesMediaJobWithEmptyList(t *testing.T) {
	c := validContent()
	c.PhotoURLs = nil
	h := newHarness(c)
	_ = h.parse()
	if len(h.pub.mediaCalls) != 1 || len(h.pub.mediaCalls[0].urls) != 0 {
		t.Errorf("mediaCalls=%+v — Media получает пустой список и сама отвечает n/a", h.pub.mediaCalls)
	}
}

func TestParse_BlockedContent_SavesItemBlockedAndStopsPipeline(t *testing.T) {
	reason := "captcha stub page"
	// Заглушка: ни заголовка, ни цены — валидация НЕ должна на неё срабатывать.
	h := newHarness(Extracted{BlockedReason: &reason})
	if err := h.parse(); err != nil {
		t.Fatalf("err=%v — заглушка это состояние Item (гейт A), а не ошибка обработки", err)
	}

	if len(h.writer.upserts) != 1 || h.writer.upserts[0].BlockedReason == nil || *h.writer.upserts[0].BlockedReason != reason {
		t.Errorf("upserts=%+v — Item должен сохраниться с blocked_reason", h.writer.upserts)
	}
	if len(h.pub.mediaCalls)+len(h.pub.geocodeCalls)+len(h.pub.aiCalls) != 0 {
		t.Error("заблокированный Item не должен идти дальше по pipeline")
	}
	if stage, status, ok := h.events.last(); !ok || stage != "parse" || status != "blocked" {
		t.Errorf("последнее событие = (%q,%q), want (parse,blocked)", stage, status)
	}
}

func TestParse_MissingRequiredFields_IsPermanentAndSavesNothing(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Extracted)
		wantMsg string
	}{
		{"нет заголовка", func(e *Extracted) { e.Title = "  " }, "title"},
		{"нет цены", func(e *Extracted) { e.Price = 0 }, "price"},
		{"нет обоих", func(e *Extracted) { e.Title, e.Price = "", -1 }, "title, price"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validContent()
			tc.mutate(&c)
			h := newHarness(c)

			err := h.parse()
			if !shareddomain.IsPermanent(err) {
				t.Fatalf("err=%v, want permanent — повтор заведомо даст ту же невалидную карточку", err)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("err=%q не называет проблемные поля (%s)", err, tc.wantMsg)
			}
			if len(h.writer.upserts) != 0 || len(h.pub.mediaCalls) != 0 {
				t.Error("невалидная карточка не должна ни сохраняться, ни идти дальше")
			}
		})
	}
}

func TestParse_ExtractorError_IsPermanent(t *testing.T) {
	h := newHarness(Extracted{})
	h.ex.err = errors.New("not html")
	if err := h.parse(); !shareddomain.IsPermanent(err) {
		t.Errorf("err=%v, want permanent", err)
	}
	if len(h.writer.upserts) != 0 {
		t.Error("ничего не должно сохраняться")
	}
}

// Регрессия (нашлась при написании первых тестов Parse): item.ID обязан
// доходить до КАЖДОЙ публикации, причём тот, что вернула БД — при конфликте
// по (source_id, external_id) это УЖЕ существующий id, а не candidate от Fetch.
func TestParse_PublishesUseIDReturnedByWriterNotCandidate(t *testing.T) {
	existing := uuid.MustParse("00000000-0000-0000-0000-0000000000e1")
	h := newHarness(validContent())
	h.writer.overrideID = &existing

	if err := h.parse(); err != nil {
		t.Fatal(err)
	}
	if got := h.pub.mediaCalls[0].itemID; got != existing {
		t.Errorf("media itemID=%s, want %s (существующий)", got, existing)
	}
	if got := h.pub.geocodeCalls[0].itemID; got != existing {
		t.Errorf("geocode itemID=%s, want %s", got, existing)
	}
	if got := h.pub.aiCalls[0].itemID; got != existing {
		t.Errorf("ai itemID=%s, want %s", got, existing)
	}
	if h.writer.upserts[0].ID != testItemID {
		t.Errorf("в Upsert ушёл id=%s, want candidate %s", h.writer.upserts[0].ID, testItemID)
	}
}

func TestParse_UpsertError_IsTransientAndSkipsPublish(t *testing.T) {
	h := newHarness(validContent())
	h.writer.err = errors.New("db down")

	err := h.parse()
	if err == nil || shareddomain.IsPermanent(err) {
		t.Fatalf("err=%v, want временную (БД вернётся) — не в DLQ", err)
	}
	if len(h.pub.mediaCalls)+len(h.pub.geocodeCalls)+len(h.pub.aiCalls) != 0 {
		t.Error("publish-вызовы не должны происходить, если Upsert упал")
	}
}

func TestParse_PublishError_Propagates(t *testing.T) {
	h := newHarness(validContent())
	h.pub.err = errors.New("broker nacked")
	if err := h.parse(); err == nil {
		t.Fatal("ошибка публикации не должна теряться — иначе Item сохранён, а дальше не пошёл, и никто не узнает")
	}
}
