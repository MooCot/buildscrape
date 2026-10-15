// Package outbound (fetch) — outbound-реализации портов fetch/domain.
// Мок ProxyProvider (CLAUDE.md §2 — реальная ротация платных прокси не
// входит в демо осознанно).
package outbound

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"buildscrape/internal/fetch/domain"
)

// ErrNoLiveProxy — все прокси пула сейчас в бане. Для вызывающего это
// временное состояние (баны истекают) — FetchOne уводит сообщение в backoff.
var ErrNoLiveProxy = errors.New("no live proxy in pool")

// MockProxyProvider — пул фейковых прокси с round-robin и баном на срок.
// Endpoint вида "mock-proxy-N" — не URL: HTTPPageFetcher видит в нём не
// настоящий прокси и ходит напрямую, а здесь имитируется только жизненный
// цикл (выдача, бан, оживление), на котором стоит логика Fetch.
type MockProxyProvider struct {
	mu        sync.Mutex
	pool      []domain.Proxy
	deadUntil map[string]time.Time
	next      int
	now       func() time.Time
}

func NewMockProxyProvider(n int) *MockProxyProvider {
	p := &MockProxyProvider{deadUntil: map[string]time.Time{}, now: time.Now}
	for i := 0; i < n; i++ {
		p.pool = append(p.pool, domain.Proxy{Endpoint: fmt.Sprintf("mock-proxy-%d", i)})
	}
	return p
}

// Acquire — следующий живой прокси по кругу; забаненные пропускаются.
func (m *MockProxyProvider) Acquire(ctx context.Context) (domain.Proxy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	for i := 0; i < len(m.pool); i++ {
		p := m.pool[(m.next+i)%len(m.pool)]
		if until, banned := m.deadUntil[p.Endpoint]; banned && now.Before(until) {
			continue
		}
		m.next = (m.next + i + 1) % len(m.pool)
		return p, nil
	}
	return domain.Proxy{}, ErrNoLiveProxy
}

// MarkDead — бан на срок d; повторный бан не сокращает уже действующий.
func (m *MockProxyProvider) MarkDead(ctx context.Context, p domain.Proxy, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	until := m.now().Add(d)
	if cur, ok := m.deadUntil[p.Endpoint]; !ok || until.After(cur) {
		m.deadUntil[p.Endpoint] = until
	}
}
