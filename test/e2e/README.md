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

**Candidates not yet written** (CLAUDE.md "Не сделано" — named risks that
only a real broker/DB can confirm): `ClaimDue` under concurrent schedulers,
the retry ladder's `x-dead-letter-exchange: ""` requeue trick, the Parse-
upsert `CASE` not clobbering an already-`blocked_reason` row, the FK on
`pipeline_events.item_id` (MODEL §1.5bis), circuit breaker state surviving
a process restart.
