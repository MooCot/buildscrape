package observability

// NoopRecorder — см. db.NoopPool doc-comment — тот же принцип.
type NoopRecorder struct{}

func NewNoopRecorder() *NoopRecorder { return &NoopRecorder{} }

func (r *NoopRecorder) IncProcessed(stage, status string)             {}
func (r *NoopRecorder) ObserveDuration(stage string, seconds float64) {}
func (r *NoopRecorder) SetQueueLength(queue string, n int)            {}
