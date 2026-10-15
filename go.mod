module buildscrape

go 1.23

// Версии — те же, что зафиксированы в CLAUDE.md ("Зафиксированные версии").
// prometheus/client_golang (v1.20.5) сюда намеренно не входит, пока
// internal/platform/observability на NoopRecorder: `go mod tidy` убирает
// неимпортируемое — добавить `go get github.com/prometheus/client_golang@v1.20.5`
// вместе с реальными метриками.

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.1
	github.com/rabbitmq/amqp091-go v1.10.0
	github.com/spf13/cobra v1.8.1
	golang.org/x/net v0.30.0
	golang.org/x/sync v0.8.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/text v0.19.0 // indirect
)
