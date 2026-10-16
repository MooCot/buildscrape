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

- `TestParseUpsert_BlockedRefetchDoesNotClobberGoodContent` — the `CASE`
  in `item_repo.go`'s upsert (MODEL-buildscrape.md §1.3 "Важное
  исправление", C10/P7, added 2026-10-16): a stale refetch that lands on
  a CAPTCHA/stub page must not clobber the previously-good
  title/description/price/address — only `blocked_reason` updates. A
  genuine model gap, same class as `ClaimDue` (E2E-2): the Entity and
  gate already existed in §1, this specific write-side invariant for it
  had simply never been written down as a C#.

**Candidates not yet written** (CLAUDE.md "Не сделано" — named risks that
only a real broker/DB can confirm): the FK on `pipeline_events.item_id`
(MODEL §1.5bis), circuit breaker state surviving a process restart.

**testcontainers-go rabbitmq module gotchas (v0.33.0), all worked around
in `harness.go`:**
- Two INDEPENDENT 60s timeouts have to be raised, not one — raising only
  one still gives an effective 60s ceiling (this is exactly how the test
  flaked intermittently before both were found):
  1. `wait.LogStrategy.timeout` (wait/log.go) — if not set via
     `.WithStartupTimeout(...)` on the strategy itself, `WaitUntilReady`
     derives its own 60s `context.WithTimeout`, independent of any outer
     deadline.
  2. `wait.MultiStrategy.deadline` (wait/all.go, set via
     `ForAll(...).WithDeadline(...)`) — the outer ceiling across all
     strategies; `testcontainers.WithWaitStrategy(...)` hardcodes this to
     60s regardless of the strategy's own timeout — use
     `WithWaitStrategyAndDeadline(deadline, strategy)` instead, with the
     SAME longer duration passed to both layers.
  Needed at all because this machine's RabbitMQ boot time varies wildly
  (observed anywhere from ~27s to ~85s across runs) — the real
  docker-compose stack showed the same thing earlier this session.
- `rabbitmq.WithAdminUsername/WithAdminPassword` write `default_user`/
  `default_pass` into the config file, but `Run()` also hardcodes
  `RABBITMQ_DEFAULT_USER/PASS=guest` in the container env — in practice
  `guest`/`guest` wins. Don't fight it; the test doesn't care about
  credentials.
- `TESTCONTAINERS_RYUK_DISABLED=true` has a real cost: if `rabbitmq.Run`
  itself fails (e.g. the deadline above being too short) before
  `t.Cleanup(ctr.Terminate)` gets registered, the container is orphaned
  — nothing else will stop it. Several timeout failures while debugging
  the above left 6 running RabbitMQ containers competing for this
  machine's resources, which made subsequent container boots even
  slower (a feedback loop). Check `docker ps -a --filter
  label=org.testcontainers=true` after any failed e2e run on this
  machine and remove stragglers by hand.
