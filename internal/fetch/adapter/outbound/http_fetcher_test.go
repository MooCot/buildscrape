package outbound

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"buildscrape/internal/fetch/domain"
)

func TestHTTPFetcher_ReturnsStatusAndBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("User-Agent не выставлен — часть источников режет пустой")
		}
		_, _ = w.Write([]byte("hello"))
	}))
	defer ts.Close()

	page, err := NewHTTPPageFetcher(5*time.Second).Get(context.Background(), domain.Proxy{Endpoint: "mock-proxy-0"}, ts.URL)
	if err != nil || page.Status != 200 || page.Body != "hello" {
		t.Errorf("page=%+v err=%v", page, err)
	}
}

func TestHTTPFetcher_ErrorStatusIsPageNotError(t *testing.T) {
	// Классификация 404/429/5xx — забота домена, транспорт обязан отдать
	// статус, а не превратить его в error.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer ts.Close()

	page, err := NewHTTPPageFetcher(5*time.Second).Get(context.Background(), domain.Proxy{}, ts.URL)
	if err != nil {
		t.Fatalf("err=%v — 429 это Page, не транспортная ошибка", err)
	}
	if page.Status != 429 || page.RetryAfter != 3*time.Second {
		t.Errorf("page=%+v, want status 429 и RetryAfter 3s", page)
	}
}

func TestHTTPFetcher_ConnectionRefusedIsError(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	url := ts.URL
	ts.Close() // порт закрыт

	if _, err := NewHTTPPageFetcher(2*time.Second).Get(context.Background(), domain.Proxy{}, url); err == nil {
		t.Error("want транспортную ошибку")
	}
}

func TestHTTPFetcher_RealProxyEndpointIsActuallyUsed(t *testing.T) {
	var seenHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Forward-прокси получает запрос с абсолютным URL целевого хоста.
		seenHost = r.Host
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	page, err := NewHTTPPageFetcher(5*time.Second).Get(context.Background(), domain.Proxy{Endpoint: proxy.URL}, "http://target.invalid/listings/1")
	if err != nil || page.Body != "via-proxy" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if seenHost != "target.invalid" {
		t.Errorf("прокси увидел host=%q, want target.invalid — запрос не пошёл через прокси", seenHost)
	}
}

func TestHTTPFetcher_OversizedBodyIsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer ts.Close()

	f := NewHTTPPageFetcher(5 * time.Second)
	f.maxBody = 50
	if _, err := f.Get(context.Background(), domain.Proxy{}, ts.URL); err == nil {
		t.Error("тело больше лимита должно быть ошибкой, а не молча обрезанной страницей")
	}
}

func TestHTTPFetcher_ContextCancelAborts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := NewHTTPPageFetcher(10*time.Second).Get(ctx, domain.Proxy{}, ts.URL); err == nil {
		t.Fatal("want error")
	}
	if time.Since(start) > 3*time.Second {
		t.Error("запрос не прервался по ctx — graceful shutdown воркера зависал бы на медленном источнике")
	}
}

func TestProxyURL(t *testing.T) {
	cases := map[string]bool{
		"mock-proxy-0":               false, // фейковое имя из мока
		"":                           false,
		"http://10.0.0.1:3128":       true,
		"socks5://user:p@10.0.0.1:1": true,
		"ftp://10.0.0.1":             false,
	}
	for in, want := range cases {
		if got := proxyURL(in) != nil; got != want {
			t.Errorf("proxyURL(%q) настоящий=%v, want %v", in, got, want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	cases := map[string]time.Duration{"3": 3 * time.Second, "": 0, "abc": 0, "-5": 0, "Wed, 21 Oct 2026 07:28:00 GMT": 0}
	for in, want := range cases {
		if got := retryAfter(in); got != want {
			t.Errorf("retryAfter(%q)=%v, want %v", in, got, want)
		}
	}
}
