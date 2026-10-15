# e2e tests

Integration tests via `testcontainers-go` — spin up real Postgres/
RabbitMQ/MinIO in Docker, verify a message's full path through the real
adapter layer (not through the fake ports used by unit tests next to the
`domain` code).

A separate, slow command (`make test-e2e`), not included in `make test-unit` —
see `Makefile`. Empty until the first stage is implemented; the first
candidate is `items` upsert idempotency (`UNIQUE(source_id, external_id)` +
redelivery of the same message, ТЕОРИЯ-УГЛУБЛЁННО.md §6.2) — this can only be
verified against a real Postgres, a fake port won't reproduce `ON CONFLICT`
behavior.
