package outbound

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"buildscrape/internal/mocksource"
)

func serve(t *testing.T, opts mocksource.Options) *httptest.Server {
	t.Helper()
	opts.Sleep = func(time.Duration) {}
	ts := httptest.NewServer(mocksource.New(opts).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func page(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// Контрактный тест "мок-источник ↔ парсер": КАЖДАЯ страница, которую
// mocksource реально отдаёт (оба шаблона, с адресом и без, с фото и без),
// обязана разбираться обратно в ровно те данные, из которых была отрисована.
func TestExtractor_RoundTripsEveryListingMockSourceRenders(t *testing.T) {
	const n = 120
	ts := serve(t, mocksource.Options{Count: n})

	var ex HTMLExtractor
	var noAddr, noPhotos, templateB int
	for id := 1; id <= n; id++ {
		want := mocksource.Generate(id)
		got, err := ex.Extract(page(t, ts.URL+"/listings/"+strconv.Itoa(id)))
		if err != nil {
			t.Fatalf("id=%d: %v", id, err)
		}
		if got.BlockedReason != nil {
			t.Fatalf("id=%d: обычная страница распознана как заглушка", id)
		}
		if got.Title != want.Title || got.Price != float64(want.Price) || got.Address != want.Address || got.Description != want.Description {
			t.Fatalf("id=%d:\n got  %+v\n want %+v", id, got, want)
		}
		wantPhotos := want.PhotoURLs(ts.URL)
		if len(wantPhotos) == 0 {
			wantPhotos = nil
		}
		if !reflect.DeepEqual(got.PhotoURLs, wantPhotos) {
			t.Fatalf("id=%d: photos=%v, want %v", id, got.PhotoURLs, wantPhotos)
		}

		if want.Address == "" {
			noAddr++
		}
		if len(want.PhotoNames) == 0 {
			noPhotos++
		}
		if id%3 == 0 {
			templateB++
		}
	}
	// Тест обязан реально пройти по всем веткам, иначе "round-trip" пустой.
	if noAddr == 0 || noPhotos == 0 || templateB == 0 {
		t.Errorf("корпус не покрыл ветки: без адреса=%d без фото=%d шаблон B=%d", noAddr, noPhotos, templateB)
	}
}

func TestExtractor_CaptchaStubBecomesBlocked(t *testing.T) {
	ts := serve(t, mocksource.Options{Count: 5, CaptchaProb: 1})

	got, err := HTMLExtractor{}.Extract(page(t, ts.URL+"/listings/1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.BlockedReason == nil || *got.BlockedReason != captchaReason {
		t.Errorf("BlockedReason=%v, want %q", got.BlockedReason, captchaReason)
	}
	if got.Title != "" || got.Price != 0 || len(got.PhotoURLs) != 0 {
		t.Errorf("у заглушки не должно быть полей карточки: %+v", got)
	}
}

func TestExtractor_UnrecognizedPageYieldsEmptyFieldsNotError(t *testing.T) {
	// "Страница есть, а полей нет" — невалидная карточка, её отклоняет домен
	// (permanent → DLQ с понятной причиной); экстрактор не должен подменять
	// это ошибкой парсинга.
	got, err := HTMLExtractor{}.Extract(`<html><body><p>Ведутся технические работы</p></body></html>`)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got.Title != "" || got.Price != 0 || got.BlockedReason != nil {
		t.Errorf("got=%+v", got)
	}
}

func TestExtractor_TemplatePriorityAOverB(t *testing.T) {
	// Оба маркера на одной странице (шаблон в процессе миграции): побеждает A.
	got, _ := HTMLExtractor{}.Extract(`<h2 itemprop="name">из B</h2><h1 class="title">из A</h1>`)
	if got.Title != "из A" {
		t.Errorf("Title=%q, want приоритет шаблона A", got.Title)
	}
}

func TestExtractor_DeduplicatesPhotos(t *testing.T) {
	got, _ := HTMLExtractor{}.Extract(`<img class="photo" src="http://h/1.png"><img class="photo" src="http://h/1.png"><img class="photo" src="">`)
	if !reflect.DeepEqual(got.PhotoURLs, []string{"http://h/1.png"}) {
		t.Errorf("photos=%v", got.PhotoURLs)
	}
}

func TestParsePrice(t *testing.T) {
	cases := map[string]float64{
		"1 250 000 ₴":           1250000,
		"1\u00a0250\u00a0000 ₴": 1250000, // неразрывные пробелы
		"500 000 ₴":             500000,
		"1 250,50 грн":          1250.5,
		"1.250.000":             1250000, // точка как разделитель разрядов
		"1,250,000":             1250000,
		"99.9":                  99.9,
		"цена договорная":       0,
		"":                      0,
		"0":                     0,
	}
	for in, want := range cases {
		if got := parsePrice(in); got != want {
			t.Errorf("parsePrice(%q)=%v, want %v", in, got, want)
		}
	}
}
