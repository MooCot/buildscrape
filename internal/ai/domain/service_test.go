package domain

import (
	"context"
	"errors"
	"testing"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

var (
	testItemID  = uuid.New()
	testItemID2 = uuid.New()
)

// --- fakes (не mock — реальная, пусть и упрощённая, логика в памяти;
// см. ТЕОРИЯ-УГЛУБЛЁННО.md §1.8 — fake ловит гонки состояния, mock только
// факт вызова) ---

type fakeGate struct{ blocked bool }

func (g *fakeGate) IsBlocked(ctx context.Context, itemID uuid.UUID) (bool, error) {
	return g.blocked, nil
}

type fakeClassifier struct {
	name   string
	err    error
	result Classification
	calls  int
}

func (c *fakeClassifier) Classify(ctx context.Context, description string) (Classification, error) {
	c.calls++
	if c.err != nil {
		return Classification{}, c.err
	}
	return c.result, nil
}

type fakeBreakerStore struct{ saved map[string]BreakerSnapshot }

func newFakeBreakerStore() *fakeBreakerStore {
	return &fakeBreakerStore{saved: map[string]BreakerSnapshot{}}
}

func (s *fakeBreakerStore) Load(ctx context.Context, provider string) (BreakerSnapshot, error) {
	return s.saved[provider], nil // отсутствие ключа → zero value → Closed, как и должно быть
}

func (s *fakeBreakerStore) Save(ctx context.Context, provider string, snap BreakerSnapshot) error {
	s.saved[provider] = snap
	return nil
}

type fakeItemAIWriter struct {
	saved     *Classification
	blockedID uuid.UUID
}

func (w *fakeItemAIWriter) SaveClassification(ctx context.Context, itemID uuid.UUID, c Classification) error {
	cp := c
	w.saved = &cp
	return nil
}

func (w *fakeItemAIWriter) Block(ctx context.Context, itemID uuid.UUID, reason string) error {
	w.blockedID = itemID
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

// --- тесты ---

func TestClassifyItem_BlockedSkipsAllProviders(t *testing.T) {
	openai := &fakeClassifier{name: "openai", result: Classification{Category: "x"}}
	writer := &fakeItemAIWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{blocked: true},
		[]Provider{{Name: "openai", Classifier: openai}},
		newFakeBreakerStore(), writer, events)

	if err := svc.ClassifyItem(context.Background(), testItemID, "desc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if openai.calls != 0 {
		t.Errorf("classifier.calls=%d, want 0 — заблокированный Item не должен доходить до провайдера (C3/C8)", openai.calls)
	}
	if writer.saved != nil {
		t.Error("writer.saved != nil — классификация не должна писаться для заблокированного Item")
	}
	if stage, status, ok := events.last(); !ok || stage != "ai" || status != "blocked" {
		t.Errorf("последнее событие = (%q,%q), want (ai,blocked)", stage, status)
	}
}

func TestClassifyItem_FirstProviderSuccess_SecondNeverCalled(t *testing.T) {
	openai := &fakeClassifier{result: Classification{Category: "cat-a"}}
	mock := &fakeClassifier{result: Classification{Category: "cat-b"}}
	writer := &fakeItemAIWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, []Provider{
		{Name: "openai", Classifier: openai},
		{Name: "mock", Classifier: mock},
	}, newFakeBreakerStore(), writer, events)

	if err := svc.ClassifyItem(context.Background(), testItemID, "desc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.calls != 0 {
		t.Errorf("fallback-провайдер вызван %d раз(а), want 0 — первый уже отработал успешно", mock.calls)
	}
	if writer.saved == nil || writer.saved.Category != "cat-a" {
		t.Errorf("сохранён результат %+v, want от первого провайдера (cat-a)", writer.saved)
	}
	if stage, status, ok := events.last(); !ok || stage != "ai" || status != "done" {
		t.Errorf("последнее событие = (%q,%q), want (ai,done)", stage, status)
	}
}

func TestClassifyItem_FirstFails_FallsBackToSecond(t *testing.T) {
	openai := &fakeClassifier{err: errors.New("openai 500")}
	mock := &fakeClassifier{result: Classification{Category: "fallback"}}
	writer := &fakeItemAIWriter{}
	svc := NewService(&fakeGate{}, []Provider{
		{Name: "openai", Classifier: openai},
		{Name: "mock", Classifier: mock},
	}, newFakeBreakerStore(), writer, &fakeEventPublisher{})

	if err := svc.ClassifyItem(context.Background(), testItemID, "desc"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if writer.saved == nil || writer.saved.Category != "fallback" {
		t.Errorf("сохранён результат %+v, want от fallback-провайдера", writer.saved)
	}
}

// Ключевой тест на per-provider изоляцию (ради которой breaker был
// переделан с общего на per-provider, см. обсуждение в чате): провал
// ИМЕННО openai по порогу не должен блокировать mock — открывается только
// свой breaker, не общий.
func TestClassifyItem_OneProviderCircuitOpen_DoesNotAffectSibling(t *testing.T) {
	failing := &fakeClassifier{err: errors.New("always fails")}
	sibling := &fakeClassifier{result: Classification{Category: "ok"}}
	store := newFakeBreakerStore()
	writer := &fakeItemAIWriter{}
	svc := NewService(&fakeGate{}, []Provider{
		{Name: "failing", Classifier: failing},
		{Name: "sibling", Classifier: sibling},
	}, store, writer, &fakeEventPublisher{})

	ctx := context.Background()
	// Открываем breaker "failing" изнутри — CircuitBreakerFailureThreshold
	// последовательных вызовов ClassifyItem, каждый падает на failing,
	// откатывается на sibling (успех) — таким образом "failing" копит
	// провалы, а "sibling" каждый раз восстанавливает СВОЙ breaker в Closed.
	for i := 0; i < 10; i++ { // заведомо больше порога
		if err := svc.ClassifyItem(ctx, testItemID, "desc"); err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
	}
	if failing.calls == 0 {
		t.Fatal("test setup: failing-провайдер ни разу не был вызван")
	}
	if sibling.calls != 10 {
		t.Errorf("sibling.calls=%d, want 10 — sibling должен был отработать на КАЖДОЙ итерации (fallback), его breaker не должен был открыться", sibling.calls)
	}
	if got := store.saved["sibling"].State; got != Closed {
		t.Errorf("breaker sibling = %v, want Closed — провалы failing не должны просачиваться на sibling", got)
	}
}

// BreakerStore-персистентность: состояние breaker переживает "рестарт"
// (новый *service поверх того же store).
func TestClassifyItem_BreakerStateSurvivesRestart(t *testing.T) {
	failing := &fakeClassifier{err: errors.New("boom")}
	store := newFakeBreakerStore()
	ctx := context.Background()

	svc1 := NewService(&fakeGate{}, []Provider{{Name: "p", Classifier: failing}}, store, &fakeItemAIWriter{}, &fakeEventPublisher{})
	for i := 0; i < 10; i++ { // открываем breaker с запасом
		_ = svc1.ClassifyItem(ctx, testItemID, "desc")
	}
	if got := store.saved["p"].State; got != Open {
		t.Fatalf("test setup: breaker в store = %v, want Open", got)
	}
	if store.saved["p"].OpenedAt.IsZero() {
		// Именно эта проверка ловит первоначальный баг: если бы Save
		// персистировал только State (как было до находки), OpenedAt здесь
		// был бы zero-value, и следующая проверка ниже (fail-fast после
		// рестарта) проходила бы "случайно верно", маскируя реальный дефект
		// — гидратированный Open с zero-value OpenedAt на самом деле сразу
		// пропускал бы пробный вызов, а не блокировал его.
		t.Fatal("store.saved[\"p\"].OpenedAt is zero — снимок не несёт момент открытия breaker")
	}

	// "рестарт процесса" — новый service, тот же store, свежий классификатор,
	// который был бы готов ответить успехом, если бы его вызвали.
	revived := &fakeClassifier{result: Classification{Category: "would-succeed"}}
	svc2 := NewService(&fakeGate{}, []Provider{{Name: "p", Classifier: revived}}, store, &fakeItemAIWriter{}, &fakeEventPublisher{})
	err := svc2.ClassifyItem(ctx, testItemID2, "desc")
	if !errors.Is(err, ErrAllProvidersUnavailable) {
		t.Errorf("err=%v, want ErrAllProvidersUnavailable — гидратированный из store Open-breaker должен fail-fast сразу после рестарта, не давать новую серию попыток", err)
	}
	if revived.calls != 0 {
		t.Errorf("revived.calls=%d, want 0 — Open-breaker должен был заблокировать вызов ДО обращения к провайдеру", revived.calls)
	}
}

var _ shareddomain.ItemGateChecker = (*fakeGate)(nil) // компиляторная проверка соответствия интерфейсу
