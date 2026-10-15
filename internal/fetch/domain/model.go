// Package domain (fetch) — F1: планирование опроса источников (F1a) и
// загрузка страниц воркерами (F1b). Не импортирует ничего, кроме stdlib +
// internal/shared/domain + internal/core (CLAUDE.md §8, правило зависимостей).
package domain

import (
	"time"

	"github.com/google/uuid"
)

// RawItem — атомарная сущность (MODEL-buildscrape.md §1.6, "Шаг 0"): один
// писатель (F1→F2, последовательные переходы), композиционная матрица не
// нужна — только таблица переходов. ExternalID — идентификатор ИЗ
// ИСТОЧНИКА (не обязательно UUID), остаётся string намеренно.
type RawItem struct {
	ID         uuid.UUID
	SourceID   uuid.UUID
	ExternalID string
	RawHTML    string
	FetchedAt  time.Time
	Status     RawItemStatus
}

type RawItemStatus string

const (
	RawItemFetched RawItemStatus = "fetched"
	RawItemParsed  RawItemStatus = "parsed"
	RawItemFailed  RawItemStatus = "failed"
)

// Page — результат HTTP-запроса. Сетевая ошибка (нет ответа вообще) — это
// error из PageFetcher.Get, а не Page: статус-код есть только у ответа.
type Page struct {
	Status     int
	Body       string
	RetryAfter time.Duration // из заголовка Retry-After (429/503), 0 — не указан
}

// Listing — одно объявление, найденное на странице индекса. ExternalID
// вычленяет site-специфичный IndexExtractor (где в URL лежит id — знание
// конкретной вёрстки/схемы ссылок, не домена).
type Listing struct {
	URL        string
	ExternalID string
}

// IndexPage — разобранная страница индекса: объявления + ссылка на
// следующую страницу ("" — последняя).
type IndexPage struct {
	Listings []Listing
	Next     string
}
