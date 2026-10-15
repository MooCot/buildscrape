.PHONY: build test test-unit test-e2e lint run-serve run-worker compose-up compose-down

build:
	go build -o bin/buildscrape ./cmd/buildscrape

# Юнит-тесты — только *_test.go рядом с кодом (fake-порты, без Docker/сети,
# ТЕОРИЯ-УГЛУБЛЁННО.md §1.8/§5.3).
test-unit:
	go test ./internal/... ./test/inprocess/... -race

# Интеграционные — test/e2e/, testcontainers, реальные Postgres/RabbitMQ.
# Осознанно отдельная, медленная команда — не часть test-unit.
test-e2e:
	go test ./test/e2e/... -race -tags=e2e

test: test-unit test-e2e

lint:
	golangci-lint run ./...

run-serve:
	go run ./cmd/buildscrape serve

run-worker:
	go run ./cmd/buildscrape worker $(STAGE)

compose-up:
	docker compose -f deployments/docker-compose.yml up --build

compose-down:
	docker compose -f deployments/docker-compose.yml down -v
