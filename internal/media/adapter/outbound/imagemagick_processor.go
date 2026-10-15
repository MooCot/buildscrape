package outbound

import (
	"context"

	"buildscrape/internal/media/domain"
)

// ImageMagickProcessor — TODO: реальный вызов ImageMagick/libvips (CLAUDE.md
// §6, "Media-воркер").
type ImageMagickProcessor struct{}

func NewImageMagickProcessor() *ImageMagickProcessor { return &ImageMagickProcessor{} }

func (p *ImageMagickProcessor) Process(ctx context.Context, sourceURL string, kind domain.MediaKind) ([]byte, error) {
	return nil, nil
}
