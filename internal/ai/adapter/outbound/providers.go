// Package outbound (ai) — Classifier-реализации + BreakerStore + ItemAIWriter
// (CLAUDE.md §3.5).
package outbound

import (
	"context"

	"buildscrape/internal/ai/domain"
)

// OpenAI/Alibaba — TODO: реальные HTTP-клиенты к LLM-провайдерам.
type OpenAIClassifier struct{ apiKey string }

func NewOpenAIClassifier(apiKey string) *OpenAIClassifier { return &OpenAIClassifier{apiKey: apiKey} }

func (c *OpenAIClassifier) Classify(ctx context.Context, description string) (domain.Classification, error) {
	return domain.Classification{}, nil
}

type AlibabaClassifier struct{ apiKey string }

func NewAlibabaClassifier(apiKey string) *AlibabaClassifier {
	return &AlibabaClassifier{apiKey: apiKey}
}

func (c *AlibabaClassifier) Classify(ctx context.Context, description string) (domain.Classification, error) {
	return domain.Classification{}, nil
}

// MockClassifier — демо без ключей, fallback по умолчанию.
type MockClassifier struct{}

func NewMockClassifier() *MockClassifier { return &MockClassifier{} }

func (c *MockClassifier) Classify(ctx context.Context, description string) (domain.Classification, error) {
	return domain.Classification{Category: "unknown"}, nil
}
