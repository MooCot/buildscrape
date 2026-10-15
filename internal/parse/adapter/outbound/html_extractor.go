package outbound

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"buildscrape/internal/parse/domain"
	"buildscrape/internal/platform/htmlx"

	"golang.org/x/net/html"
)

// HTMLExtractor — экстрактор карточки для источника формата мок-сайта
// (internal/mocksource). Site-специфичность — ровно то, зачем это адаптер за
// портом domain.Extractor: новый источник — новый экстрактор, домен не
// меняется. Поддерживает ОБА шаблона разметки мока (один сайт меняет
// вёрстку — норма, а не исключение): для каждого поля перебираются
// селекторы в порядке приоритета.
type HTMLExtractor struct{}

type selector func(*html.Node) bool

func byClass(c string) selector { return func(n *html.Node) bool { return htmlx.HasClass(n, c) } }
func byAttr(k, v string) selector {
	return func(n *html.Node) bool { return n.Type == html.ElementNode && htmlx.Attr(n, k) == v }
}

var (
	titleSelectors   = []selector{byClass("title"), byAttr("itemprop", "name")}
	priceSelectors   = []selector{byClass("price"), byAttr("data-role", "price")}
	addressSelectors = []selector{byClass("address"), byAttr("itemprop", "address")}
	descSelectors    = []selector{byClass("description"), byClass("text")}
)

const captchaReason = "captcha stub page"

func (HTMLExtractor) Extract(raw string) (domain.Extracted, error) {
	doc, err := htmlx.Parse(raw)
	if err != nil {
		return domain.Extracted{}, err
	}

	// Заглушка проверяется ПЕРВОЙ: у неё нет полей карточки, и без этой
	// проверки она превратилась бы в "невалидную карточку" (permanent → DLQ)
	// вместо того, чтобы стать заблокированным Item (гейт A).
	if htmlx.Find(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && htmlx.Attr(n, "id") == "captcha-container"
	}) != nil {
		reason := captchaReason
		return domain.Extracted{BlockedReason: &reason}, nil
	}

	return domain.Extracted{
		Title:       firstText(doc, titleSelectors),
		Price:       parsePrice(firstText(doc, priceSelectors)),
		Address:     firstText(doc, addressSelectors),
		Description: firstText(doc, descSelectors),
		PhotoURLs:   photoURLs(doc),
	}, nil
}

// firstText — текст первого найденного элемента; селекторы перебираются по
// приоритету (шаблон A, потом шаблон B), а не "любой из".
func firstText(doc *html.Node, sels []selector) string {
	for _, sel := range sels {
		if n := htmlx.Find(doc, sel); n != nil {
			return htmlx.Text(n)
		}
	}
	return ""
}

// photoURLs — <img class="photo" src> (шаблон A) и <a data-photo href>
// (шаблон B) в порядке документа, без пустых и повторов.
func photoURLs(doc *html.Node) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, n := range htmlx.FindAll(doc, func(n *html.Node) bool {
		return (htmlx.Tag(n, "img") && htmlx.HasClass(n, "photo")) || (htmlx.Tag(n, "a") && hasAttr(n, "data-photo"))
	}) {
		if htmlx.Tag(n, "img") {
			add(htmlx.Attr(n, "src"))
		} else {
			add(htmlx.Attr(n, "href"))
		}
	}
	return out
}

func hasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

var priceToken = regexp.MustCompile(`\d[\d.,]*`)

// parsePrice: "1 250 000 ₴" → 1250000; "1 250,50 грн" → 1250.5. Разделители
// разрядов (пробел, NBSP, точка/запятая внутри числа) выкидываются; хвост
// "[.,]" + 1-2 цифры считается десятичной частью. Нет числа → 0 (домен
// отклонит такую карточку как невалидную).
func parsePrice(s string) float64 {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1 // включая NBSP: "1 250 000" с неразрывными пробелами
		}
		return r
	}, s)
	tok := priceToken.FindString(compact)
	if tok == "" {
		return 0
	}

	intPart, frac := tok, ""
	if i := strings.LastIndexAny(tok, ".,"); i >= 0 && len(tok)-i-1 >= 1 && len(tok)-i-1 <= 2 {
		intPart, frac = tok[:i], tok[i+1:]
	}
	intPart = strings.NewReplacer(".", "", ",", "").Replace(intPart)
	if frac != "" {
		intPart += "." + frac
	}
	v, err := strconv.ParseFloat(intPart, 64)
	if err != nil {
		return 0
	}
	return v
}
