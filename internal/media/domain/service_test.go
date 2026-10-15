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

type fakeProcessor struct {
	err   error
	calls []MediaKind
}

func (p *fakeProcessor) Process(ctx context.Context, sourceURL string, kind MediaKind) ([]byte, error) {
	p.calls = append(p.calls, kind)
	if p.err != nil {
		return nil, p.err
	}
	return []byte("data"), nil
}

type fakeStore struct {
	err  error
	puts int
}

func (s *fakeStore) Put(ctx context.Context, key string, data []byte) error {
	s.puts++
	return s.err
}

type fakeMediaWriter struct {
	saves []ItemMedia
}

func (w *fakeMediaWriter) Save(ctx context.Context, m ItemMedia) error {
	w.saves = append(w.saves, m)
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

func TestProcessItem_Blocked_SkipsEverything(t *testing.T) {
	proc := &fakeProcessor{}
	store := &fakeStore{}
	writer := &fakeMediaWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{blocked: true}, proc, store, writer, events)

	if err := svc.ProcessItem(context.Background(), testItemID, []string{"http://x/1.jpg"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.calls) != 0 {
		t.Errorf("processor вызван %d раз(а), want 0 — заблокированный Item не должен обрабатываться (C3/C8)", len(proc.calls))
	}
	if store.puts != 0 || len(writer.saves) != 0 {
		t.Errorf("store.puts=%d, writer.saves=%d, want 0/0", store.puts, len(writer.saves))
	}
	if stage, status, ok := events.last(); !ok || stage != "media" || status != "blocked" {
		t.Errorf("последнее событие = (%q,%q), want (media,blocked)", stage, status)
	}
}

func TestProcessItem_NoPhotoURLs_EmitsNA(t *testing.T) {
	proc := &fakeProcessor{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, proc, &fakeStore{}, &fakeMediaWriter{}, events)

	if err := svc.ProcessItem(context.Background(), testItemID, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(proc.calls) != 0 {
		t.Errorf("processor вызван %d раз(а), want 0 — нет photo-ссылок ⟹ n/a, не 'done' (MODEL §1.6)", len(proc.calls))
	}
	if stage, status, ok := events.last(); !ok || stage != "media" || status != "n/a" {
		t.Errorf("последнее событие = (%q,%q), want (media,n/a)", stage, status)
	}
}

func TestProcessItem_ProcessesEveryKindForEveryURL(t *testing.T) {
	proc := &fakeProcessor{}
	store := &fakeStore{}
	writer := &fakeMediaWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, proc, store, writer, events)

	urls := []string{"http://x/1.jpg", "http://x/2.jpg"}
	if err := svc.ProcessItem(context.Background(), testItemID, urls); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantCalls := len(urls) * len(mediaKinds)
	if len(proc.calls) != wantCalls {
		t.Errorf("processor вызван %d раз(а), want %d (%d URL × %d MediaKind)", len(proc.calls), wantCalls, len(urls), len(mediaKinds))
	}
	if store.puts != wantCalls {
		t.Errorf("store.puts=%d, want %d", store.puts, wantCalls)
	}
	if len(writer.saves) != wantCalls {
		t.Errorf("writer.saves=%d, want %d", len(writer.saves), wantCalls)
	}
	if stage, status, ok := events.last(); !ok || stage != "media" || status != "done" {
		t.Errorf("последнее событие = (%q,%q), want (media,done)", stage, status)
	}
}

func TestProcessItem_ProcessorError_StopsAndEmitsError(t *testing.T) {
	proc := &fakeProcessor{err: errors.New("imagemagick crashed")}
	store := &fakeStore{}
	writer := &fakeMediaWriter{}
	events := &fakeEventPublisher{}
	svc := NewService(&fakeGate{}, proc, store, writer, events)

	err := svc.ProcessItem(context.Background(), testItemID, []string{"http://x/1.jpg"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if store.puts != 0 {
		t.Errorf("store.puts=%d, want 0 — Put не должен вызываться после неудачного Process", store.puts)
	}
	if len(writer.saves) != 0 {
		t.Errorf("writer.saves=%d, want 0", len(writer.saves))
	}
	if stage, status, ok := events.last(); !ok || stage != "media" || status != "error" {
		t.Errorf("последнее событие = (%q,%q), want (media,error)", stage, status)
	}
}
