// Package domain (storage) — F6 (публичная выдача) + F7/F8/F9 (admin
// Source: создание/enable-disable/архивация). Один хексагон — оба делят
// shareddomain.SourceRepository, раздельно не образуют самостоятельной
// границы (MODEL-buildscrape.md §8, СР-storage).
package domain

import "github.com/google/uuid"

// ItemView — то, что отдаёт публичное API. Lat/Lng — указатели: nil значит
// "не геокодирован" (у объявления нет адреса или Geocode ещё не отработал),
// а не координаты (0, 0) — точка в Атлантике, которую клиент нарисовал бы.
type ItemView struct {
	ID          uuid.UUID
	Title       string
	Description string
	Price       float64
	Lat, Lng    *float64
	AICategory  string
}
