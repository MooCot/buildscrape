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
(named-pipe timeout). This has a real cost, not just a cosmetic one — see
the gotchas section below.

**All 6 planned tests are done** (E2E-1 through E2E-6):

- `TestParseUpsert_RedeliveredMessageIsIdempotent` (E2E-1) — items upsert
  idempotency (`UNIQUE(source_id, external_id)` + redelivery of the same
  message, ТЕОРИЯ-УГЛУБЛЁННО.md §6.2, MODEL-buildscrape.md C1/C2/P4).
- `TestClaimDue_ConcurrentSchedulersClaimExactlyOnce` (E2E-2) — `ClaimDue`
  mutual exclusion under N concurrent schedulers (`--scale
  worker-fetch=N`), MODEL-buildscrape.md §1.1bis, C9/P6 — the model is the
  source of truth here (added 2026-10-16, this invariant used to live only
  in CLAUDE.md/code comments).
- `TestRetryLadder_DeadLetterReturnsMessageToMainQueue` (E2E-3) — the
  `x-dead-letter-exchange: ""` requeue trick (CLAUDE.md:152, retry.go);
  real RabbitMQ container, real 1-minute TTL on `fetch.queue.retry.1` —
  the slowest of the six, deliberately not scoped to `make test-unit`.
  Correctly absent from MODEL-buildscrape.md — retry/DLQ are RabbitMQ
  infrastructure, not a domain Entity/Gate (same reasoning as §1.5bis for
  metrics), unlike the other three gaps below where the model really was
  missing something.
- `TestParseUpsert_BlockedRefetchDoesNotClobberGoodContent` (E2E-4) — the
  `CASE` in `item_repo.go`'s upsert (MODEL-buildscrape.md §1.3 "Важное
  исправление", C10/P7): a stale refetch that lands on a CAPTCHA/stub page
  must not clobber the previously-good title/description/price/address —
  only `blocked_reason` updates. A third kind of gap, distinct from
  `ClaimDue`: the Entity and the gate already existed in §1, this specific
  write-side invariant for it had simply never been written down as a C#.
- `TestEventRecorder_FKBoundary` (E2E-5) — `pipeline_events.item_id`'s
  `NOT NULL REFERENCES items(id)` (MODEL §1.5) really rejects a
  nonexistent item_id, not just an assumption in the docs; also the happy
  path (item exists → row appears). No model edit needed here — the
  citation was already correct.
- `TestBreakerStore_OpenedAtSurvivesRestart` (E2E-6) — a fourth kind of
  gap, same class as `ClaimDue`: `ai_breaker_state` (provider/state/
  opened_at) is a real Entity with identity that had never been given its
  own §1 treatment (MODEL §1.4bis, C11/P9, added 2026-10-16) — only
  mentioned as a side effect in F5's row. The domain-level hydration logic
  already had unit coverage with a fake store
  (`TestClassifyItem_BreakerStateSurvivesRestart`); this test is
  specifically about `PostgresBreakerStore`'s real NULL/TIMESTAMPTZ
  round-trip, which the fake can't reproduce.

**Run this stack's own `docker compose` stack DOWN before running
`test/e2e` locally.** The demo stack (`deployments-*`, 12+ containers)
left running competes with freshly-spun test containers for this
machine's Docker Desktop VM (5.9 GB total). Measured difference: Postgres
container boot went from ~20-40s down to ~8s, and the full 6-test suite
from 327-430s down to 168s, just from `docker compose -f
deployments/docker-compose.yml down` first. `test/e2e` never touches the
compose stack either way — its containers are fully disposable and
separate.

**testcontainers-go rabbitmq module gotchas (v0.33.0), all worked around
in `harness.go`:**
- Two INDEPENDENT timeouts have to be raised, not one — raising only one
  still gives an effective ceiling at whichever is smaller (this is
  exactly how the test flaked intermittently before both were found):
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
  Both are set to 3 minutes in `harness.go`. Needed because this
  machine's RabbitMQ boot time varies wildly — observed from ~27s up to
  "Time to start RabbitMQ: 122062 ms" in the broker's own log, before it
  even reaches "Server startup complete" — almost certainly resource
  contention with whatever else Docker Desktop is running (see above).
- `rabbitmq.WithAdminUsername/WithAdminPassword` write `default_user`/
  `default_pass` into the config file, but `Run()` also hardcodes
  `RABBITMQ_DEFAULT_USER/PASS=guest` in the container env — in practice
  `guest`/`guest` wins. Don't fight it; the test doesn't care about
  credentials.
- `TESTCONTAINERS_RYUK_DISABLED=true` has a real cost: if `rabbitmq.Run`
  itself fails (e.g. the deadline above being too short) before
  `t.Cleanup(ctr.Terminate)` gets registered, the container is orphaned
  — nothing else will stop it. This happened repeatedly while debugging
  the timeout above (once leaving 6 containers running at once), and each
  orphan made every subsequent container boot slower still (a feedback
  loop, on top of the compose-stack contention above). Check `docker ps
  -a --filter label=org.testcontainers=true` after any failed e2e run on
  this machine and remove stragglers by hand — `docker rm -f <name>`.
  `-count=2` (or any repeated run) in the same `go test` invocation
  multiplies this risk; prefer separate invocations if a run fails.
