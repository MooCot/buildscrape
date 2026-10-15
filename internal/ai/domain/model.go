// Package domain (ai) — F5: классификация описания через LLM. Может писать
// одну из сторон Шва гейта A (blocked_reason), если контент нарушает
// политику (MODEL-buildscrape.md §1.3).
package domain

type Classification struct {
	Category string
	Tags     []string
}

// Provider — именованный Classifier. Имя — ключ, под которым breaker-состояние
// персистится в BreakerStore ("per provider", CLAUDE.md §8 doc-comment на
// postgres_breaker.go) — один общий breaker на все классификаторы сразу не
// отражал бы это: сбой конкретно OpenAI не должен открывать breaker и для
// Alibaba/Mock тоже.
type Provider struct {
	Name       string
	Classifier Classifier
}
