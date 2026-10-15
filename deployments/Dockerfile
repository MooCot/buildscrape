# Multi-stage build → distroless (ТЕОРИЯ-УГЛУБЛЁННО.md §4.1) — build-стадия
# с полным тулчейном Go, финальный образ содержит только бинарник.
FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/buildscrape ./cmd/buildscrape

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/buildscrape /buildscrape
ENTRYPOINT ["/buildscrape"]
