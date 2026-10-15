package outbound

import (
	"context"

	"buildscrape/internal/geocode/domain"
)

// GeocoderAPI — TODO: мок или realistic-rate-limited stub (CLAUDE.md §3.4).
type GeocoderAPI struct{}

func NewGeocoderAPI() *GeocoderAPI { return &GeocoderAPI{} }

func (g *GeocoderAPI) Resolve(ctx context.Context, address string) (domain.GeoResult, error) {
	return domain.GeoResult{}, nil
}
