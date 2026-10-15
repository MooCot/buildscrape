package domain

import (
	"testing"
	"time"

	"buildscrape/internal/core"
)

func TestCircuitBreaker_ClosedStaysClosedOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker()
	for i := 0; i < 10; i++ {
		if !cb.Allow() {
			t.Fatalf("iteration %d: Allow()=false, хотя breaker должен быть Closed", i)
		}
		cb.RecordSuccess()
	}
	if got := cb.State(); got != Closed {
		t.Errorf("State()=%v, want Closed", got)
	}
}

func TestCircuitBreaker_SuccessResetsConsecutiveFailures(t *testing.T) {
	// Ниже порога, потом успех, потом снова ниже порога — НЕ должно открыться:
	// RecordSuccess обнуляет счётчик, а не просто "разрешает ещё один провал".
	cb := NewCircuitBreaker()
	for i := 0; i < core.CircuitBreakerFailureThreshold-1; i++ {
		cb.RecordFailure()
	}
	cb.RecordSuccess()
	for i := 0; i < core.CircuitBreakerFailureThreshold-1; i++ {
		cb.RecordFailure()
	}
	if got := cb.State(); got != Closed {
		t.Fatalf("State()=%v, want Closed — успех между сериями провалов должен был сбросить счётчик", got)
	}
}

func TestCircuitBreaker_OpensExactlyAtThreshold(t *testing.T) {
	cb := NewCircuitBreaker()
	for i := 1; i <= core.CircuitBreakerFailureThreshold; i++ {
		cb.RecordFailure()
		wantOpen := i == core.CircuitBreakerFailureThreshold
		gotOpen := cb.State() == Open
		if gotOpen != wantOpen {
			t.Fatalf("после %d/%d провалов подряд: State()=%v (Open=%v), want Open=%v",
				i, core.CircuitBreakerFailureThreshold, cb.State(), gotOpen, wantOpen)
		}
	}
}

func TestCircuitBreaker_OpenFailsFastBeforeCooldown(t *testing.T) {
	cb := openBreaker(t)
	if cb.Allow() {
		t.Fatal("Allow()=true сразу после открытия (cooldown не мог истечь) — ожидался fail-fast")
	}
}

func TestCircuitBreaker_HalfOpenAfterCooldown_SuccessCloses(t *testing.T) {
	cb := openBreaker(t)
	expireCooldown(cb)

	if !cb.Allow() {
		t.Fatal("Allow()=false после истечения cooldown — ожидался один пробный вызов (HalfOpen)")
	}
	if got := cb.State(); got != HalfOpen {
		t.Fatalf("State()=%v, want HalfOpen сразу после первого Allow() по истечении cooldown", got)
	}
	cb.RecordSuccess()
	if got := cb.State(); got != Closed {
		t.Fatalf("State()=%v, want Closed после успешной пробы в HalfOpen", got)
	}
}

func TestCircuitBreaker_HalfOpenFailureReopens(t *testing.T) {
	cb := openBreaker(t)
	expireCooldown(cb)
	cb.Allow() // переход Open → HalfOpen, пробный вызов "выдан"

	cb.RecordFailure()
	if got := cb.State(); got != Open {
		t.Fatalf("State()=%v, want Open — неудачная проба в HalfOpen должна вернуть breaker в Open", got)
	}
}

// Регрессионный тест на конкретную находку из этой сессии (см. Allow() doc-
// comment): второй конкурентный Allow() во время уже идущей HalfOpen-пробы
// обязан быть fail-fast, а не выдавать ещё один пробный вызов.
func TestCircuitBreaker_HalfOpenAllowsOnlyOneConcurrentTrial(t *testing.T) {
	cb := openBreaker(t)
	expireCooldown(cb)

	first := cb.Allow()  // единственный законный пробный вызов
	second := cb.Allow() // должен быть отклонён — проба уже "выдана" и не разрешена

	if !first {
		t.Fatal("первый Allow() после истечения cooldown должен быть true")
	}
	if second {
		t.Fatal("второй Allow() во время незавершённой HalfOpen-пробы должен быть false (не два пробных вызова разом)")
	}
}

// --- test helpers ---

func openBreaker(t *testing.T) *CircuitBreaker {
	t.Helper()
	cb := NewCircuitBreaker()
	for i := 0; i < core.CircuitBreakerFailureThreshold; i++ {
		cb.RecordFailure()
	}
	if got := cb.State(); got != Open {
		t.Fatalf("test setup: State()=%v, want Open после %d провалов подряд", got, core.CircuitBreakerFailureThreshold)
	}
	return cb
}

// expireCooldown — прямая манипуляция unexported-полем вместо time.Sleep на
// реальный CircuitBreakerCooldown (30с, internal/core) — тест в том же
// пакете (белый ящик), не требует делать cooldown injectable ради теста.
func expireCooldown(cb *CircuitBreaker) {
	cb.mu.Lock()
	cb.openedAt = time.Now().Add(-core.CircuitBreakerCooldown - time.Second)
	cb.mu.Unlock()
}
