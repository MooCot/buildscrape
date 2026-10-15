package domain

import (
	"sync"
	"time"

	"buildscrape/internal/core"
)

// State — closed/open/half-open. Чистая state-machine, 0 внешних
// зависимостей — обычные unit-тесты без Docker/сети (ТЕОРИЯ-УГЛУБЛЁННО.md
// §8.1).
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

type CircuitBreaker struct {
	mu              sync.Mutex
	state           State
	consecutiveFail int
	openedAt        time.Time
}

func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{state: Closed}
}

// NewCircuitBreakerFromSnapshot — гидратация при старте процесса из
// BreakerStore (переживает рестарт, см. ports.go doc-comment). Snapshot
// несёт и OpenedAt, не только State — без него восстановленный Open-breaker
// считал бы cooldown уже истёкшим (zero-value time.Time проходит проверку
// time.Since(...) >= CircuitBreakerCooldown), см. BreakerSnapshot doc-comment.
func NewCircuitBreakerFromSnapshot(s BreakerSnapshot) *CircuitBreaker {
	return &CircuitBreaker{state: s.State, openedAt: s.OpenedAt}
}

func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Snapshot — для персистентности (BreakerStore.Save); State() одного
// недостаточно, см. NewCircuitBreakerFromSnapshot.
func (cb *CircuitBreaker) Snapshot() BreakerSnapshot {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return BreakerSnapshot{State: cb.state, OpenedAt: cb.openedAt}
}

// Allow — false означает fail-fast: во внешний вызов не идём вообще.
//
// НАЙДЕНО ПРИ НАПИСАНИИ ТЕСТА (не постфактум-ревью, а прямой продукт
// TestCircuitBreaker_*): предыдущая версия сводила HalfOpen к тому же
// "default: return true", что и Closed — при конкурентных вызовах Allow()
// (несколько goroutine на один provider) HalfOpen пропустил бы больше
// одного пробного запроса одновременно, что противоречит и комментарию "один
// пробный вызов", и классическому определению паттерна. HalfOpen теперь
// отдельная ветка: ПЕРВЫЙ Allow() после истечения cooldown переводит Open→
// HalfOpen и возвращает true; КАЖДЫЙ следующий, пока RecordSuccess/
// RecordFailure не разрешит пробу, — fail-fast (false), не второй пробный
// вызов.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case Closed:
		return true
	case Open:
		if time.Since(cb.openedAt) >= core.CircuitBreakerCooldown {
			cb.state = HalfOpen
			return true // единственный пробный вызов — переход случается ровно здесь
		}
		return false
	case HalfOpen:
		return false // проба уже идёт — остальные ждут её исхода, не дублируют
	default:
		return false
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFail = 0
	cb.state = Closed
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFail++
	if cb.state == HalfOpen || cb.consecutiveFail >= core.CircuitBreakerFailureThreshold {
		cb.state = Open
		cb.openedAt = time.Now()
	}
}
