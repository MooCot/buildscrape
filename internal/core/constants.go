// Package core — shared kernel. Значения, нужные нескольким domain-пакетам
// сразу, живут здесь, а не в одном из них, — иначе остальные вынуждены
// импортировать "чужую" стадию напрямую (см. CLAUDE.md §8/§9).
//
// core/** не импортирует ничего своего (ни domain, ни adapter, ни
// platform) — иначе он перестаёт быть самым нижним слоем.
package core

import "time"

// Circuit breaker defaults — единственный источник для internal/ai/domain
// и любой будущей стадии с собственным breaker-guarded внешним вызовом
// (geocode rate limits — очевидный следующий кандидат).
const (
	CircuitBreakerFailureThreshold = 5
	CircuitBreakerCooldown         = 30 * time.Second
)

// Очередь / retry — exponential backoff (1 / 2 / 4 мин) как единственный
// источник, чтобы не рассыпать те же числа по воркерам inline-литералами.
const (
	DefaultPrefetchCount = 1
	DefaultBatchSize     = 5
	MaxRetryAttempts     = 3
	RetryBaseDelay       = 1 * time.Minute
)

// TTL для RawItem — консультируется F10/cleanup (internal/cleanup/domain),
// см. MODEL-buildscrape.md §1.6 "RawItem — решённая термінація".
const RawItemTTL = 72 * time.Hour
