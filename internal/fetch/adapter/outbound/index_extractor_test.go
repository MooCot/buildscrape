package outbound

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"buildscrape/internal/mocksource"
)

func fetchBody(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// Контрактный тест "мок-источник ↔ экстрактор": страница, которую реально
// отдаёт mocksource, обязана разбираться обратно в ровно те объявления.
func TestIndexExtractor_ParsesWhatMockSourceServes(t *testing.T) {
	ts := httptest.NewServer(mocksource.New(mocksource.Options{Count: 120, PageSize: 50, Sleep: func(time.Duration) {}}).Handler())
	defer ts.Close()

	var ex HTMLIndexExtractor
	total, url, pages := 0, ts.URL+"/listings", 0
	seen := map[string]bool{}
	for url != "" && pages < 10 {
		idx, err := ex.ExtractIndex(fetchBody(t, url), url)
		if err != nil {
			t.Fatalf("page %d: %v", pages+1, err)
		}
		for _, l := range idx.Listings {
			if seen[l.ExternalID] {
				t.Fatalf("external_id %s повторился", l.ExternalID)
			}
			seen[l.ExternalID] = true
			if l.URL != ts.URL+"/listings/"+l.ExternalID {
				t.Errorf("URL=%q не соответствует external_id=%q", l.URL, l.ExternalID)
			}
		}
		total += len(idx.Listings)
		pages++
		url = idx.Next
	}
	if total != 120 || pages != 3 {
		t.Errorf("total=%d pages=%d, want 120/3", total, pages)
	}
}

func TestIndexExtractor_ResolvesRelativeLinks(t *testing.T) {
	body := `<ul class="listings"><li><a class="listing-link" href="/listings/7">x</a></li></ul><a rel="next" href="?page=2">n</a>`
	idx, err := HTMLIndexExtractor{}.ExtractIndex(body, "http://src.example/listings?page=1")
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Listings) != 1 || idx.Listings[0].URL != "http://src.example/listings/7" || idx.Listings[0].ExternalID != "7" {
		t.Errorf("listings=%+v", idx.Listings)
	}
	if idx.Next != "http://src.example/listings?page=2" {
		t.Errorf("next=%q", idx.Next)
	}
}

func TestIndexExtractor_LastPageHasNoNext(t *testing.T) {
	body := `<ul class="listings"><li><a class="listing-link" href="http://h/l/1">x</a></li></ul>`
	idx, err := HTMLIndexExtractor{}.ExtractIndex(body, "http://h/listings")
	if err != nil || idx.Next != "" {
		t.Errorf("next=%q err=%v, want пусто", idx.Next, err)
	}
}

func TestIndexExtractor_NonIndexPageIsErrorNotEmptyIndex(t *testing.T) {
	captcha := `<html><body><div id="captcha-container">robot?</div></body></html>`
	if _, err := (HTMLIndexExtractor{}).ExtractIndex(captcha, "http://h/listings"); err == nil {
		t.Error("страница-заглушка вместо индекса не должна выглядеть как 'ноль объявлений'")
	}
}

func TestIndexExtractor_EmptyButValidIndexIsNotError(t *testing.T) {
	idx, err := HTMLIndexExtractor{}.ExtractIndex(`<ul class="listings"></ul>`, "http://h/listings")
	if err != nil || len(idx.Listings) != 0 {
		t.Errorf("listings=%d err=%v — пустой валидный индекс легитимен", len(idx.Listings), err)
	}
}

func TestIndexExtractor_SkipsBrokenHrefs(t *testing.T) {
	body := `<ul class="listings"><li><a class="listing-link" href="">пусто</a></li><li><a class="listing-link" href="http://h/l/2">ok</a></li></ul>`
	idx, _ := HTMLIndexExtractor{}.ExtractIndex(body, "http://h/listings")
	if len(idx.Listings) != 1 || idx.Listings[0].ExternalID != "2" {
		t.Errorf("listings=%+v", idx.Listings)
	}
}
