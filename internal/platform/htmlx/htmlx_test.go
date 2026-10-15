package htmlx

import (
	"testing"

	"golang.org/x/net/html"
)

const doc = `<html><body>
<div class="a b" id="x"><p>  Привет,
   <b>мир</b> </p></div>
<ul><li class="item">1</li><li class="item">2</li><li>3</li></ul>
</body></html>`

func mustParse(t *testing.T) *html.Node {
	t.Helper()
	n, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestHasClass_MatchesOneOfSeveral(t *testing.T) {
	root := mustParse(t)
	div := Find(root, func(n *html.Node) bool { return Tag(n, "div") })
	if !HasClass(div, "a") || !HasClass(div, "b") || HasClass(div, "ab") || HasClass(div, "c") {
		t.Error("HasClass должен сравнивать класс целиком, а не подстрокой")
	}
}

func TestText_CollapsesWhitespaceAcrossNestedNodes(t *testing.T) {
	root := mustParse(t)
	p := Find(root, func(n *html.Node) bool { return Tag(n, "p") })
	if got := Text(p); got != "Привет, мир" {
		t.Errorf("Text=%q, want %q", got, "Привет, мир")
	}
}

func TestFindAll_DocumentOrder(t *testing.T) {
	root := mustParse(t)
	items := FindAll(root, func(n *html.Node) bool { return HasClass(n, "item") })
	if len(items) != 2 || Text(items[0]) != "1" || Text(items[1]) != "2" {
		t.Errorf("items=%d, want 2 в порядке документа", len(items))
	}
}

func TestFind_NoMatchIsNil(t *testing.T) {
	if Find(mustParse(t), func(n *html.Node) bool { return Tag(n, "table") }) != nil {
		t.Error("отсутствующий элемент должен давать nil, а не панику")
	}
}

func TestAttr_MissingIsEmpty(t *testing.T) {
	div := Find(mustParse(t), func(n *html.Node) bool { return Tag(n, "div") })
	if Attr(div, "id") != "x" || Attr(div, "href") != "" {
		t.Error("Attr")
	}
}

func TestParse_ToleratesBrokenMarkup(t *testing.T) {
	// Незакрытые теги/мусор — обычная жизнь скрапинга; парсер не должен падать.
	n, err := Parse(`<div><p>текст<b>жирный</div><span`)
	if err != nil || n == nil {
		t.Fatalf("err=%v", err)
	}
}
