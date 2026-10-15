# buildscrape

Event-driven pipeline for collecting/enriching/serving data (Go + RabbitMQ +
PostgreSQL), a modular monolith with independently scalable worker roles,
hexagonal architecture (ports & adapters).

## Running the demo

```bash
make compose-up          # postgres/rabbitmq/minio/prometheus/grafana + mock-source + all roles
```

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
components; SQL and AMQP aren't included (that's `test/e2e`, still empty).

## Structure

```
cmd/buildscrape/           composition root (serve / worker <stage> / mock-source)
internal/<stage>/          domain (no external dependencies) + adapter/{inbound,outbound}
internal/shared/domain/    domain ports and error sentinels shared across stages (ItemGateChecker, SourceRepository, ErrPermanent)
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
