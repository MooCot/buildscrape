# buildscrape

Event-driven pipeline for collecting/enriching/serving data (Go + RabbitMQ +
PostgreSQL), a modular monolith with independently scalable worker roles,
hexagonal architecture (ports & adapters).

## Running the demo

```bash
make compose-up          # postgres/rabbitmq/prometheus/grafana + mock-source + all roles
```

MinIO was dropped from the compose file (October 2025 — `minio/minio` stopped serving anonymous pulls on both Docker Hub and quay.io); `MinIOStore` is a no-op stub regardless, so nothing is lost by its absence yet.

On this host, bringing up all ~12 containers in one shot can make RabbitMQ miss its healthcheck window under the simultaneous I/O load (`.erlang.cookie: eacces`, see git history) — if that happens, bring up `postgres rabbitmq` first, wait for `healthy`, then `up -d` the rest.

The demo source (`mock-source`, port 9090) serves 300 fake listings with
unstable markup, 429/503s and CAPTCHA stubs. For the pipeline to start
polling it, register it as a source:

```bash
curl -X POST http://localhost:8080/admin/sources \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $ADMIN_TOKEN" \      # if ADMIN_TOKEN is unset — the header isn't needed
  -d '{"name":"mock","base_url":"http://mock-source:9090/listings","poll_interval_sec":60}'
```

Within ~30 seconds the Fetch scheduler picks up the source, spreads the
listings onto `fetch.queue`, and the workers download and parse them:

```bash
curl http://localhost:8080/items?q=house          # public listing feed
curl -X PATCH http://localhost:8080/admin/sources/<id>/enabled -d '{"enabled":false}'   # pause
```

Queues, retry stages and the DLQ — in the RabbitMQ management UI: http://localhost:15672
(`buildscrape` / `buildscrape`). Scaling: `docker compose -f
deployments/docker-compose.yml up --scale worker-fetch=10`.

Without Docker (roles start on `Noop` DB/queue stubs if
`POSTGRES_DSN`/`RABBITMQ_URL` are unset):

```bash
make run-serve
make run-worker STAGE=parse
go run ./cmd/buildscrape mock-source --listings 50 --captcha-prob 0.1
```

## Checking without infrastructure

```bash
make test-unit    # unit + contract + in-process end-to-end (real HTTP, in-memory DB/queue)
```

`test/inprocess` runs the mock-source → Fetch → Parse chain on real
components; SQL and AMQP aren't included (that's `test/e2e`, still empty —
see "Verified on live infrastructure" below for a manual run against real
Postgres/RabbitMQ).

## Verified on live infrastructure (2026-10-16)

A full run against real Postgres + RabbitMQ (`make compose-up`, no fakes) —
not yet automated as `test/e2e`, done manually once to validate the riskiest
code (`ClaimDue` under concurrency, retry dead-lettering, the Parse upsert):

- All 300 mock-source listings went end to end: Fetch → Parse → Media /
  Geocode / AI, zero permanent failures except one pre-existing `fetch.dlq`
  entry unrelated to this run.
- Scaling demo (roadmap step 2), run to the end of the chain, not just the
  first stage: `--scale worker-fetch=10` drained `fetch.queue` but pushed
  the backlog onto `parse.queue` (1 worker); `--scale worker-parse=5` drained
  that and pushed it onto `media`/`geocode`/`ai.queue` (1 worker each);
  `--scale worker-media=5 --scale worker-geocode=5 --scale worker-ai=5`
  finally drained everything to zero. `media` turned out to be the heaviest
  single stage (its per-item loop over photos × 3 output sizes). End state:
  300/300 items processed, 56081 rows in `pipeline_events` tracing every
  stage transition of the run.

## Structure

```
cmd/buildscrape/           composition root (serve / worker <stage> / mock-source)
internal/<stage>/          domain (no external dependencies) + adapter/{inbound,outbound}
internal/shared/domain/    domain ports and error sentinels shared across stages (ItemGateChecker, SourceRepository, ErrPermanent)
internal/shared/adapter/   EventRecorder — the one PublishStageEvent implementation for parse/media/geocode/ai (writes pipeline_events, publishes pipeline.events best-effort)
internal/shared/contracts/ inter-stage message format (RabbitMQ) — adapter-only, not imported by domain
internal/platform/         infrastructure wrappers (queue/db/config/observability/htmlx)
internal/core/             immutable core — constants + append-only pipeline version registry
internal/mocksource/       demo data source (a fixture, not a pipeline role)
migrations/                SQL schema
deployments/               Dockerfile + docker-compose + prometheus.yml
test/inprocess/            end-to-end tests without infrastructure
test/e2e/                  integration tests (testcontainers), still empty
```

## Commands

See `Makefile` — `build`, `test-unit`, `test-e2e`, `lint`, `run-serve`,
`run-worker STAGE=<stage>`, `compose-up`/`compose-down`.
