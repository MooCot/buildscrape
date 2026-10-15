package mocksource

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	if opts.Sleep == nil {
		opts.Sleep = func(time.Duration) {} // тесты не спят
	}
	ts := httptest.NewServer(New(opts).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestGenerate_DeterministicPerID(t *testing.T) {
	if !reflect.DeepEqual(Generate(42), Generate(42)) {
		t.Error("тот же id обязан давать те же данные — иначе повторный опрос видел бы 'новый' объект")
	}
	if reflect.DeepEqual(Generate(1), Generate(2)) {
		t.Error("разные id дали идентичные данные — генератор не зависит от id")
	}
}

func TestGenerate_ProducesEdgeCasesAcrossCorpus(t *testing.T) {
	// Пайплайну нужны и объявления без адреса (Geocode → n/a), и без фото
	// (Media → n/a): иначе эти ветки на демо-данных не выполнялись бы никогда.
	var noAddr, noPhoto, withBoth int
	for id := 1; id <= 300; id++ {
		l := Generate(id)
		switch {
		case l.Address == "":
			noAddr++
		case len(l.PhotoNames) == 0:
			noPhoto++
		default:
			withBoth++
		}
		if l.Title == "" || l.Price <= 0 {
			t.Fatalf("id=%d: пустые обязательные поля: %+v", id, l)
		}
	}
	if noAddr == 0 || noPhoto == 0 || withBoth == 0 {
		t.Errorf("корпус однообразен: noAddr=%d noPhoto=%d full=%d", noAddr, noPhoto, withBoth)
	}
}

func TestIndex_PaginatesAllListings(t *testing.T) {
	ts := newTestServer(t, Options{Count: 120, PageSize: 50})

	total, url, pages := 0, ts.URL+"/listings", 0
	for url != "" && pages < 10 {
		status, body, _ := get(t, url)
		if status != 200 {
			t.Fatalf("GET %s: %d", url, status)
		}
		total += strings.Count(body, `class="listing-link"`)
		pages++

		url = ""
		if i := strings.Index(body, `rel="next" href="`); i >= 0 {
			rest := body[i+len(`rel="next" href="`):]
			url = rest[:strings.Index(rest, `"`)]
		}
	}
	if total != 120 || pages != 3 {
		t.Errorf("total=%d pages=%d, want 120/3", total, pages)
	}
}

func TestListing_OutOfRangeIs404(t *testing.T) {
	ts := newTestServer(t, Options{Count: 10})
	for _, path := range []string{"/listings/0", "/listings/11", "/listings/abc"} {
		if status, _, _ := get(t, ts.URL+path); status != 404 {
			t.Errorf("%s: status=%d, want 404", path, status)
		}
	}
}

func TestListing_RendersBothTemplates(t *testing.T) {
	ts := newTestServer(t, Options{Count: 10})

	_, a, _ := get(t, ts.URL+"/listings/1") // 1%3 != 0 → шаблон A
	_, b, _ := get(t, ts.URL+"/listings/3") // 3%3 == 0 → шаблон B
	if !strings.Contains(a, `class="title"`) || strings.Contains(a, `itemprop="name"`) {
		t.Error("id=1 должен рендериться шаблоном A")
	}
	if !strings.Contains(b, `itemprop="name"`) || strings.Contains(b, `class="title"`) {
		t.Error("id=3 должен рендериться шаблоном B")
	}
}

func TestChaos_RateLimitAlways(t *testing.T) {
	ts := newTestServer(t, Options{Count: 5, RateLimitProb: 1})

	status, _, hdr := get(t, ts.URL+"/listings/1")
	if status != 429 || hdr.Get("Retry-After") == "" {
		t.Errorf("status=%d Retry-After=%q, want 429 с Retry-After", status, hdr.Get("Retry-After"))
	}
	if status, _, _ := get(t, ts.URL+"/listings"); status != 429 {
		t.Errorf("индекс: status=%d, want 429 — хаос касается и индекса", status)
	}
}

func TestChaos_ServerErrorAlways(t *testing.T) {
	ts := newTestServer(t, Options{Count: 5, ServerErrProb: 1})
	if status, _, _ := get(t, ts.URL+"/listings/1"); status != 503 {
		t.Errorf("status=%d, want 503", status)
	}
}

func TestChaos_CaptchaIsServedAs200(t *testing.T) {
	ts := newTestServer(t, Options{Count: 5, CaptchaProb: 1})

	status, body, _ := get(t, ts.URL+"/listings/1")
	if status != 200 {
		t.Errorf("status=%d, want 200 — заглушка выдаётся за контент, статус её не выдаёт", status)
	}
	if !strings.Contains(body, `id="captcha-container"`) || strings.Contains(body, `class="title"`) {
		t.Error("вместо объявления должна прийти страница-заглушка")
	}
}

func TestChaos_ZeroProbabilitiesNeverFail(t *testing.T) {
	ts := newTestServer(t, Options{Count: 50, Seed: 1})
	for id := 1; id <= 50; id++ {
		if status, _, _ := get(t, ts.URL+"/listings/"+strconv.Itoa(id)); status != 200 {
			t.Fatalf("id=%d: status=%d при нулевых вероятностях хаоса", id, status)
		}
	}
}

func TestPhoto_ServesPNG(t *testing.T) {
	ts := newTestServer(t, Options{})
	status, body, hdr := get(t, ts.URL+"/photos/1-1.png")
	if status != 200 || hdr.Get("Content-Type") != "image/png" || !strings.HasPrefix(body, "\x89PNG") {
		t.Errorf("status=%d content-type=%q — Media-стадии нужен настоящий PNG", status, hdr.Get("Content-Type"))
	}
}

func TestGroupThousands(t *testing.T) {
	cases := map[int]string{500000: "500 000", 1250000: "1 250 000", 999: "999", 1000: "1 000"}
	for in, want := range cases {
		if got := groupThousands(in); got != want {
			t.Errorf("groupThousands(%d)=%q, want %q", in, got, want)
		}
	}
}
