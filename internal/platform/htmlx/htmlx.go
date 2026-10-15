// Package htmlx — тонкие хелперы поверх golang.org/x/net/html для
// site-специфичных экстракторов (индекс в Fetch, карточка в Parse).
// Лежит в platform, а не в одном из адаптеров: адаптеры разных стадий друг
// друга импортировать не могут (CLAUDE.md §8), а обходы дерева нужны обоим.
package htmlx

import (
	"strings"

	"golang.org/x/net/html"
)

// Parse — html.Parse из строки. Парсер HTML5 терпим к "нестабильной"
// разметке (незакрытые теги, мусор) — ошибка возможна только на чтении.
func Parse(s string) (*html.Node, error) {
	return html.Parse(strings.NewReader(s))
}

// Find — первый (в порядке обхода в глубину) элемент, удовлетворяющий pred.
func Find(root *html.Node, pred func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(root, func(n *html.Node) bool {
		if pred(n) {
			found = n
			return false
		}
		return true
	})
	return found
}

// FindAll — все элементы, удовлетворяющие pred, в порядке документа.
func FindAll(root *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	walk(root, func(n *html.Node) bool {
		if pred(n) {
			out = append(out, n)
		}
		return true
	})
	return out
}

// walk — обход в глубину; fn вернул false — остановить весь обход.
func walk(n *html.Node, fn func(*html.Node) bool) bool {
	if !fn(n) {
		return false
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if !walk(c, fn) {
			return false
		}
	}
	return true
}

func Attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// HasClass — у элемента есть класс class (атрибут class — список через пробел).
func HasClass(n *html.Node, class string) bool {
	if n.Type != html.ElementNode {
		return false
	}
	for _, c := range strings.Fields(Attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// Tag — элемент с тегом tag.
func Tag(n *html.Node, tag string) bool {
	return n.Type == html.ElementNode && n.Data == tag
}

// Text — весь текст внутри узла, с схлопнутыми пробелами/переводами строк.
func Text(n *html.Node) string {
	var b strings.Builder
	walk(n, func(c *html.Node) bool {
		if c.Type == html.TextNode {
			b.WriteString(c.Data)
			b.WriteByte(' ')
		}
		return true
	})
	return strings.Join(strings.Fields(b.String()), " ")
}
