package outbound

import (
	"errors"
	"net/url"
	"path"
	"strings"

	"buildscrape/internal/fetch/domain"
	"buildscrape/internal/platform/htmlx"

	"golang.org/x/net/html"
)

// HTMLIndexExtractor — экстрактор индекса для источника формата мок-сайта
// (internal/mocksource): контейнер <ul class="listings">, ссылки
// <a class="listing-link">, пагинация <a rel="next">, external_id — последний
// сегмент пути. Site-специфичность — ровно то, зачем это адаптер за портом
// domain.IndexExtractor: новый источник — новый экстрактор, домен не меняется.
type HTMLIndexExtractor struct{}

var errNotIndex = errors.New("страница не похожа на индекс объявлений (нет контейнера ul.listings)")

func (HTMLIndexExtractor) ExtractIndex(body, pageURL string) (domain.IndexPage, error) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return domain.IndexPage{}, err
	}
	doc, err := htmlx.Parse(body)
	if err != nil {
		return domain.IndexPage{}, err
	}

	// Страница без контейнера — не "пустой индекс", а не индекс вовсе
	// (CAPTCHA-заглушка, страница ошибки с кодом 200). Молча вернуть ноль
	// объявлений значило бы, что источник тихо "ничего не публикует".
	container := htmlx.Find(doc, func(n *html.Node) bool { return htmlx.Tag(n, "ul") && htmlx.HasClass(n, "listings") })
	if container == nil {
		return domain.IndexPage{}, errNotIndex
	}

	var out domain.IndexPage
	for _, a := range htmlx.FindAll(container, func(n *html.Node) bool { return htmlx.Tag(n, "a") && htmlx.HasClass(n, "listing-link") }) {
		u, ok := resolve(base, htmlx.Attr(a, "href"))
		if !ok {
			continue
		}
		out.Listings = append(out.Listings, domain.Listing{URL: u, ExternalID: lastSegment(u)})
	}
	if next := htmlx.Find(doc, func(n *html.Node) bool { return htmlx.Tag(n, "a") && htmlx.Attr(n, "rel") == "next" }); next != nil {
		out.Next, _ = resolve(base, htmlx.Attr(next, "href"))
	}
	return out, nil
}

// resolve — ссылка относительно страницы; пустые/битые href пропускаются.
func resolve(base *url.URL, href string) (string, bool) {
	if strings.TrimSpace(href) == "" {
		return "", false
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	return base.ResolveReference(ref).String(), true
}

// lastSegment — external_id объявления: "http://h/listings/42" → "42".
func lastSegment(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return ""
	}
	return path.Base(parsed.Path)
}
