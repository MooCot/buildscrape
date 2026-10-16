package observability

import (
	"github.com/prometheus/client_golang/prometheus"
)

// PrometheusRecorder — реальная реализация Recorder (CLAUDE.md §10.5).
// Каждая роль (serve/worker <stage>) — отдельный процесс/контейнер, поэтому
// один PrometheusRecorder на процесс, регистрируемый на
// prometheus.DefaultRegisterer, без коллизий имён между ролями.
type PrometheusRecorder struct {
	processed *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	queueLen  *prometheus.GaugeVec
}

func NewPrometheusRecorder() *PrometheusRecorder {
	r := &PrometheusRecorder{
		processed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "buildscrape_items_processed_total",
			Help: "Количество обработанных сообщений по стадии и итоговому статусу.",
		}, []string{"stage", "status"}),
		// Histogram, не Summary — агрегируется корректно между инстансами
		// многоинстансового воркера (ТЕОРИЯ-УГЛУБЛЁННО.md §7.1). Бакеты —
		// prometheus.DefBuckets: своих данных по латентности этой системы
		// пока нет, чтобы их обоснованно откалибровать (CLAUDE.md §10.1).
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "buildscrape_stage_duration_seconds",
			Help:    "Длительность обработки одного сообщения стадией.",
			Buckets: prometheus.DefBuckets,
		}, []string{"stage"}),
		queueLen: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "buildscrape_queue_length",
			Help: "Текущая глубина очереди RabbitMQ (включая retry-ступени и DLQ).",
		}, []string{"queue"}),
	}
	prometheus.MustRegister(r.processed, r.duration, r.queueLen)
	return r
}

func (r *PrometheusRecorder) IncProcessed(stage, status string) {
	r.processed.WithLabelValues(stage, status).Inc()
}

func (r *PrometheusRecorder) ObserveDuration(stage string, seconds float64) {
	r.duration.WithLabelValues(stage).Observe(seconds)
}

func (r *PrometheusRecorder) SetQueueLength(queue string, n int) {
	r.queueLen.WithLabelValues(queue).Set(float64(n))
}
