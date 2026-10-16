package queue

import "buildscrape/internal/core"

// StageSpec — топология одной стадии: имя очереди, её exchange (+тип),
// routing key для bind (пусто — для direct/fanout, паттерн — для topic),
// нужен ли ей персональный dead-letter exchange/queue. Соответствует
// CLAUDE.md §4.3 буквально (типы exchange по стадиям, DLX на каждой).
type StageSpec struct {
	Queue        string
	Exchange     string
	ExchangeType string // "direct" | "topic" | "fanout"
	RoutingKey   string
	DLX          bool
	// Retry — завести wait-очереди backoff-ступеней (<queue>.retry.1..N, см.
	// retry.go). Нужно стадиям, у которых ЕСТЬ consumer очереди — у всех
	// pipeline-стадий (cleanup очереди не имеет вообще). Для Fetch это
	// backoff на 429/5xx источника.
	Retry bool
}

// Topology — реестр всех pipeline-стадий (не pipeline.events — см. ниже —
// и не cleanup, у которого нет очереди вообще, CLAUDE.md §3.7). Одна
// декларация вместо пяти разрозненных ExchangeDeclare/QueueDeclare по
// файлам стадий — топология толком имеет смысл только целиком, не
// по кускам, и сверяется с CLAUDE.md §4.3 в одном месте.
var Topology = []StageSpec{
	{Queue: "fetch.queue", Exchange: "fetch.exchange", ExchangeType: "direct", DLX: true, Retry: true},
	{Queue: "parse.queue", Exchange: "parse.exchange", ExchangeType: "topic", RoutingKey: "parse.source.*", DLX: true, Retry: true},
	{Queue: "media.queue", Exchange: "media.exchange", ExchangeType: "direct", DLX: true, Retry: true},
	{Queue: "geocode.queue", Exchange: "geocode.exchange", ExchangeType: "direct", DLX: true, Retry: true},
	{Queue: "ai.queue", Exchange: "ai.exchange", ExchangeType: "direct", DLX: true, Retry: true},
}

// PipelineEventsExchange — fanout (CLAUDE.md §4.3), без общей очереди в
// этой топологии: каждый подписчик (dashboard, будущий webhook) заводит
// СВОЮ очередь с уникальным/exclusive именем при подключении — это его
// забота при потреблении, не общей декларации при старте.
const PipelineEventsExchange = "pipeline.events"

// AllQueueNames — все очереди топологии, включая retry-ступени и DLQ, в
// одном месте (F11/П-metrics, MODEL-buildscrape.md, CLAUDE.md §10.3) —
// опрос длины очередей не собирает этот список второй раз сам, а только
// добавляет к основной очереди retry/DLQ-варианты той же формулой, что
// Declare уже использует при декларации (RetryQueueName/DLQName).
func AllQueueNames(specs []StageSpec) []string {
	names := make([]string, 0, len(specs)*(1+core.MaxRetryAttempts+1))
	for _, s := range specs {
		names = append(names, s.Queue)
		if s.Retry {
			for attempt := 1; attempt <= core.MaxRetryAttempts; attempt++ {
				names = append(names, RetryQueueName(s.Queue, attempt))
			}
		}
		if s.DLX {
			names = append(names, DLQName(s.Queue))
		}
	}
	return names
}
