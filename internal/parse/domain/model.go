// Package domain (parse) — F2: RawItem → Item, гейт A (Item.blocked_reason)
// пишущая сторона #1 (Шов с F5/ai, см. MODEL-buildscrape.md §1.3/§6 C-Шов).
package domain

import "github.com/google/uuid"

// ExternalID — идентификатор ИЗ ИСТОЧНИКА (не обязательно UUID), остаётся
// string намеренно.
type Item struct {
	ID            uuid.UUID
	SourceID      uuid.UUID
	ExternalID    string
	Title         string
	Description   string
	Price         float64
	Address       string
	BlockedReason *string // гейт A — nullable, F2 или F5 пишут (Шов)
}

// Extracted — результат разбора сырого HTML site-специфичным Extractor'ом:
// поля карточки + фото-ссылки (которые в Item не попадают — их обрабатывает
// Media, Parse лишь передаёт в MediaJob) + признак "это не контент".
type Extracted struct {
	Title       string
	Description string
	Price       float64
	Address     string
	PhotoURLs   []string

	// BlockedReason != nil — страница оказалась заглушкой прокси/CAPTCHA,
	// выданной за контент (гейт A, MODEL-buildscrape.md §1.3). Остальные поля
	// в этом случае не заполнены и не проверяются.
	BlockedReason *string
}
