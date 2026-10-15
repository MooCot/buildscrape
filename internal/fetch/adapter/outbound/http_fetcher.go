package outbound

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"buildscrape/internal/fetch/domain"
)

const defaultMaxBody = 8 << 20 // 8 МБ: страница объявления больше — уже не страница

// HTTPPageFetcher — реализует domain.PageFetcher поверх net/http.
type HTTPPageFetcher struct {
	timeout time.Duration
	maxBody int64

	mu      sync.Mutex
	clients map[string]*http.Client // ключ — endpoint прокси; "" — прямое соединение
}

func NewHTTPPageFetcher(timeout time.Duration) *HTTPPageFetcher {
	return &HTTPPageFetcher{timeout: timeout, maxBody: defaultMaxBody, clients: map[string]*http.Client{}}
}

// Get — один GET. Любой полученный ответ (включая 4xx/5xx) — это Page, а не
// ошибка: классификацию по статусу делает домен (fetch/domain/service.go
// get). error — только транспорт: нет соединения, таймаут, оборванное тело.
func (f *HTTPPageFetcher) Get(ctx context.Context, proxy domain.Proxy, target string) (domain.Page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return domain.Page{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "buildscrape/1.0")

	resp, err := f.clientFor(proxy).Do(req)
	if err != nil {
		return domain.Page{}, err
	}
	defer resp.Body.Close()

	// +1 байт сверх лимита — чтобы отличить "ровно лимит" от "больше лимита".
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBody+1))
	if err != nil {
		return domain.Page{}, fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > f.maxBody {
		return domain.Page{}, fmt.Errorf("body exceeds %d bytes", f.maxBody)
	}
	return domain.Page{
		Status:     resp.StatusCode,
		Body:       string(body),
		RetryAfter: retryAfter(resp.Header.Get("Retry-After")),
	}, nil
}

func (f *HTTPPageFetcher) clientFor(proxy domain.Proxy) *http.Client {
	pu := proxyURL(proxy.Endpoint)
	key := ""
	if pu != nil {
		key = pu.String()
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[key]; ok {
		return c
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if pu != nil {
		tr.Proxy = http.ProxyURL(pu)
	} else {
		tr.Proxy = nil // прямое соединение, не подхватывать HTTP_PROXY из окружения
	}
	c := &http.Client{Transport: tr, Timeout: f.timeout}
	f.clients[key] = c
	return c
}

// proxyURL — endpoint как URL прокси; nil, если это не настоящий прокси
// (например, "mock-proxy-0" — фейковое имя из мока, ходим напрямую).
func proxyURL(endpoint string) *url.URL {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil
	}
	switch u.Scheme {
	case "http", "https", "socks5":
		return u
	}
	return nil
}

// retryAfter — только форма "секунды" (HTTP-date у источников встречается
// редко; неразобранное значение = "не указан", домен возьмёт срок по умолчанию).
func retryAfter(h string) time.Duration {
	if secs, err := strconv.Atoi(h); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}
