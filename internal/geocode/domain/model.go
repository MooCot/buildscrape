// Package domain (geocode) — F4: адрес → координаты.
package domain

type GeoResult struct {
	Lat, Lng   float64
	GeocodedAt string
}
