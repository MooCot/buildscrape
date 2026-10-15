package outbound

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newProxies(n int) (*MockProxyProvider, *time.Time) {
	clock := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	p := NewMockProxyProvider(n)
	p.now = func() time.Time { return clock }
	return p, &clock
}

func TestMockProxy_RoundRobin(t *testing.T) {
	p, _ := newProxies(3)
	var got []string
	for i := 0; i < 6; i++ {
		px, err := p.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, px.Endpoint)
	}
	want := []string{"mock-proxy-0", "mock-proxy-1", "mock-proxy-2", "mock-proxy-0", "mock-proxy-1", "mock-proxy-2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestMockProxy_SkipsBannedAndRevivesAfterTTL(t *testing.T) {
	p, clock := newProxies(2)
	ctx := context.Background()

	first, _ := p.Acquire(ctx) // mock-proxy-0
	p.MarkDead(ctx, first, time.Minute)

	for i := 0; i < 3; i++ {
		px, err := p.Acquire(ctx)
		if err != nil || px.Endpoint != "mock-proxy-1" {
			t.Fatalf("i=%d: %v %v — забаненный прокси не должен выдаваться", i, px, err)
		}
	}

	*clock = clock.Add(61 * time.Second) // бан истёк
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		px, _ := p.Acquire(ctx)
		seen[px.Endpoint] = true
	}
	if !seen["mock-proxy-0"] {
		t.Error("прокси должен ожить по истечении бана — раньше MarkDead был вечным (TODO снять пометку так и не был сделан)")
	}
}

func TestMockProxy_AllBannedIsErrNoLiveProxy(t *testing.T) {
	p, _ := newProxies(2)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		px, _ := p.Acquire(ctx)
		p.MarkDead(ctx, px, time.Hour)
	}
	if _, err := p.Acquire(ctx); !errors.Is(err, ErrNoLiveProxy) {
		t.Errorf("err=%v, want ErrNoLiveProxy", err)
	}
}

func TestMockProxy_RepeatedBanNeverShortensExisting(t *testing.T) {
	p, clock := newProxies(1)
	ctx := context.Background()
	px, _ := p.Acquire(ctx)
	p.MarkDead(ctx, px, time.Hour)
	p.MarkDead(ctx, px, time.Second) // короткий бан поверх длинного

	*clock = clock.Add(time.Minute)
	if _, err := p.Acquire(ctx); !errors.Is(err, ErrNoLiveProxy) {
		t.Error("второй, короткий бан сократил действующий часовой")
	}
}
