package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPrometheusRecorder_RecordsValues(t *testing.T) {
	r := NewPrometheusRecorder()

	r.IncProcessed("parse", "done")
	r.IncProcessed("parse", "done")
	r.IncProcessed("parse", "error")
	r.ObserveDuration("parse", 0.5)
	r.SetQueueLength("parse.queue", 42)

	if got := testutil.ToFloat64(r.processed.WithLabelValues("parse", "done")); got != 2 {
		t.Errorf("processed{parse,done} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(r.processed.WithLabelValues("parse", "error")); got != 1 {
		t.Errorf("processed{parse,error} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(r.queueLen.WithLabelValues("parse.queue")); got != 42 {
		t.Errorf("queueLen{parse.queue} = %v, want 42", got)
	}
	// Histogram не ToFloat64-совместим напрямую (нет единого значения) —
	// достаточно, что ObserveDuration не паникует и регистрация прошла;
	// содержимое бакетов проверяется живым прогоном (CLAUDE.md §10.1).
}
