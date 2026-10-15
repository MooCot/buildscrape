// Package observability — Prometheus-метрики + structured logging, общие
// для всех ролей (CLAUDE.md §8). TODO: после `go get
// github.com/prometheus/client_golang` завести реальные Counter/Gauge/
// Histogram (НЕ Summary — см. ТЕОРИЯ-УГЛУБЛЁННО.md §7.1, почему Histogram
// обязателен на многоинстансовом воркере) и запретить unbounded labels
// (§7.2 — никогда item_id как label).
package observability

// Recorder — контракт, которым пользуется каждый inbound-адаптер для учёта
// обработанных сообщений/длительности; labels — только низкокардинальные
// (stage, status, provider), не идентификаторы сущностей.
type Recorder interface {
	IncProcessed(stage, status string)
	ObserveDuration(stage string, seconds float64)
	SetQueueLength(queue string, n int)
}
