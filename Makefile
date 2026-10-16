.PHONY: build test test-unit test-e2e lint run-serve run-worker compose-up compose-down

build:
	go build -o bin/buildscrape ./cmd/buildscrape

# Юнит-тесты — только *_test.go рядом с кодом (fake-порты, без Docker/сети,
# ТЕОРИЯ-УГЛУБЛЁННО.md §1.8/§5.3).
test-unit:
	go test ./internal/... ./test/inprocess/... -race

# Интеграционные — test/e2e/, testcontainers, реальные Postgres/RabbitMQ.
# Осознанно отдельная, медленная команда — не часть test-unit.
# TESTCONTAINERS_RYUK_DISABLED: ryuk (служебный reaper-контейнер
# testcontainers для автоочистки) не стартует на Docker Desktop/Windows в
# этом проекте (named-pipe таймаут). Имеет реальную цену: если сам
# rabbitmq.Run падает ДО t.Cleanup(ctr.Terminate), контейнер осиротевает
# (test/e2e/README.md — наблюдалось несколько раз, лечится вручную
# `docker ps -a --filter label=org.testcontainers=true`).
# -timeout 600s явно, не implicit-дефолт: RabbitMQ на этой машине грузится
# нестабильно (от ~27с до >120с по логу самого брокера), сумма 5 тестов
# несколько раз выходила за пределы более жёстких ручных -timeout.
test-e2e:
	TESTCONTAINERS_RYUK_DISABLED=true go test ./test/e2e/... -race -tags=e2e -timeout 600s

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
