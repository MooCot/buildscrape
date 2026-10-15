// Package domain (media) — F3: загрузка/обработка/заливка изображений,
// создаёт ItemMedia. Производная сущность (MODEL-buildscrape.md §1.4) —
// создание обязано консультировать гейт A/C8 перед записью (не новый гейт,
// строка в уже описанной матрице).
package domain

import "github.com/google/uuid"

type ItemMedia struct {
	ID          uuid.UUID
	ItemID      uuid.UUID
	S3Key       string
	Kind        MediaKind
	ProcessedAt string
}

type MediaKind string

const (
	MediaThumbnail MediaKind = "thumbnail"
	MediaPreview   MediaKind = "preview"
	MediaFull      MediaKind = "full"
)
