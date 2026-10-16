# e2e tests

Integration tests via `testcontainers-go` — spin up real Postgres/
RabbitMQ in Docker, verify behavior the fake ports next to `domain` code
cannot reproduce (real `ON CONFLICT`, real AMQP dead-lettering, real
concurrent locking). Scope — see `harness.go` (`setupPostgres`, applies
`migrations/*.sql` to a fresh container) and each test's doc-comment for
which documented risk it targets.

A separate, slow command (`make test-e2e`), not included in `make test-unit`
— see `Makefile`. `TESTCONTAINERS_RYUK_DISABLED=true` is set there: the
ryuk reaper sidecar container doesn't start on Docker Desktop/Windows
(named-pipe timeout) — not critical locally, each test terminates its own
container via `t.Cleanup`.

**Done:**
- `TestParseUpsert_RedeliveredMessageIsIdempotent` — items upsert
  idempotency (`UNIQUE(source_id, external_id)` + redelivery of the same
  message, ТЕОРИЯ-УГЛУБЛЁННО.md §6.2, MODEL-buildscrape.md C1/C2/P4).
- `TestClaimDue_ConcurrentSchedulersClaimExactlyOnce` — `ClaimDue`
  mutual exclusion under N concurrent schedulers (`--scale
  worker-fetch=N`), MODEL-buildscrape.md §1.1bis, C9/P6 — the model is the
  source of truth here (added 2026-10-16, this invariant used to live only
  in CLAUDE.md/code comments).
- `TestRetryLadder_DeadLetterReturnsMessageToMainQueue` — the
  `x-dead-letter-exchange: ""` requeue trick (CLAUDE.md:152, retry.go);
  real RabbitMQ container, real 1-minute TTL on `fetch.queue.retry.1` —
  slow (~90-110s including container boot), deliberately not scoped to
  `make test-unit`. Correctly absent from MODEL-buildscrape.md — retry/DLQ
  are RabbitMQ infrastructure, not a domain Entity/Gate (same reasoning as
  §1.5bis for metrics), unlike `ClaimDue` above where the gap was real.

**Candidates not yet written** (CLAUDE.md "Не сделано" — named risks that
only a real broker/DB can confirm): the Parse-upsert `CASE` not clobbering
an already-`blocked_reason` row, the FK on `pipeline_events.item_id`
(MODEL §1.5bis), circuit breaker state surviving a process restart.

**testcontainers-go rabbitmq module gotchas (v0.33.0), both worked around
in `harness.go`:**
- `testcontainers.WithWaitStrategy(...)` hardcodes a 60s deadline
  regardless of the strategy's own `WithStartupTimeout` — use
  `WithWaitStrategyAndDeadline(deadline, strategy)` to actually extend it.
  Needed here: this machine's RabbitMQ boot is slow (~30-55s), observed
  earlier this session with the real docker-compose stack too.
- `rabbitmq.WithAdminUsername/WithAdminPassword` write `default_user`/
  `default_pass` into the config file, but `Run()` also hardcodes
  `RABBITMQ_DEFAULT_USER/PASS=guest` in the container env — in practice
  `guest`/`guest` wins. Don't fight it; the test doesn't care about
  credentials.
