package mocksource

import (
	"fmt"
	"html"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options — размер и "характер" источника.
type Options struct {
	Count    int // сколько объявлений (по умолчанию 300)
	PageSize int // объявлений на страницу индекса (по умолчанию 50)

	// Хаос — вероятности на КАЖДЫЙ запрос страницы (индекса и объявления).
	RateLimitProb float64       // 429 + Retry-After: 1
	ServerErrProb float64       // 503
	CaptchaProb   float64       // 200 OK, но вместо контента — страница-заглушка (только страницы объявлений)
	MaxDelay      time.Duration // случайная задержка до MaxDelay перед ответом

	Seed  int64
	Sleep func(time.Duration) // для тестов; по умолчанию time.Sleep
}

// Server — http.Handler демо-источника.
type Server struct {
	opts Options

	mu  sync.Mutex // rand.Rand не потокобезопасен
	rng *rand.Rand
}

func New(opts Options) *Server {
	if opts.Count <= 0 {
		opts.Count = 300
	}
	if opts.PageSize <= 0 {
		opts.PageSize = 50
	}
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	return &Server{opts: opts, rng: rand.New(rand.NewSource(opts.Seed))}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /listings", s.index)
	mux.HandleFunc("GET /listings/{id}", s.listing)
	mux.HandleFunc("GET /photos/{name}", s.photo)
	return mux
}

// chaos — общая для индекса и объявлений "непредсказуемость" источника.
// Возвращает true, если ответ уже отправлен (429/503) и хендлер должен выйти.
func (s *Server) chaos(w http.ResponseWriter) (handled bool) {
	s.mu.Lock()
	roll := s.rng.Float64()
	var delay time.Duration
	if s.opts.MaxDelay > 0 {
		delay = time.Duration(s.rng.Int63n(int64(s.opts.MaxDelay)))
	}
	s.mu.Unlock()

	if delay > 0 {
		s.opts.Sleep(delay)
	}
	switch {
	case roll < s.opts.RateLimitProb:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return true
	case roll < s.opts.RateLimitProb+s.opts.ServerErrProb:
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return true
	}
	return false
}

func (s *Server) roll() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rng.Float64()
}

// baseURL — абсолютные ссылки на страницах строятся из Host запроса: парсеру
// приходит только сырой HTML (без URL страницы), относительные ссылки на фото
// он бы не разрешил.
func baseURL(r *http.Request) string { return "http://" + r.Host }

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if s.chaos(w) {
		return
	}
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 1 {
		page = p
	}
	first := (page-1)*s.opts.PageSize + 1
	last := first + s.opts.PageSize - 1
	if last > s.opts.Count {
		last = s.opts.Count
	}
	base := baseURL(r)

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><title>Объявления</title></head><body><ul class="listings">`)
	for id := first; id <= last; id++ {
		l := Generate(id)
		fmt.Fprintf(&b, `<li><a class="listing-link" href="%s/listings/%d">%s</a></li>`, base, id, html.EscapeString(l.Title))
	}
	b.WriteString(`</ul>`)
	if last < s.opts.Count {
		fmt.Fprintf(&b, `<a rel="next" href="%s/listings?page=%d">дальше</a>`, base, page+1)
	}
	b.WriteString(`</body></html>`)
	writeHTML(w, b.String())
}

func (s *Server) listing(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 || id > s.opts.Count {
		http.NotFound(w, r)
		return
	}
	if s.chaos(w) {
		return
	}
	if s.roll() < s.opts.CaptchaProb {
		// 200 OK, а не 4xx — так и ведут себя защиты: страница-проверка
		// выдаётся за контент, и статус-код её не выдаёт.
		writeHTML(w, captchaPage)
		return
	}
	writeHTML(w, renderListing(Generate(id), baseURL(r)))
}

func (s *Server) photo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(tinyPNG)
}

func writeHTML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

const captchaPage = `<!doctype html><html><head><title>Проверка безопасности</title></head>` +
	`<body><div id="captcha-container"><form action="/verify">Подтвердите, что вы не робот</form></div></body></html>`

// renderListing — два разных шаблона разметки (по id%3): источники меняют
// вёрстку, и парсер обязан переживать оба варианта, а не один "счастливый".
func renderListing(l Listing, base string) string {
	if l.ID%3 == 0 {
		return renderTemplateB(l, base)
	}
	return renderTemplateA(l, base)
}

func renderTemplateA(l Listing, base string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!doctype html><html lang="ru"><head><meta charset="utf-8"><title>%s — АКЦЕНТ-Буд</title></head><body>`, html.EscapeString(l.Title))
	fmt.Fprintf(&b, `<article class="listing" data-id="%d"><h1 class="title">%s</h1><div class="price">%s ₴</div>`, l.ID, html.EscapeString(l.Title), groupThousands(l.Price))
	if l.Address != "" {
		fmt.Fprintf(&b, `<p class="address">%s</p>`, html.EscapeString(l.Address))
	}
	fmt.Fprintf(&b, `<div class="description">%s</div>`, html.EscapeString(l.Description))
	if len(l.PhotoNames) > 0 {
		b.WriteString(`<ul class="gallery">`)
		for _, u := range l.PhotoURLs(base) {
			fmt.Fprintf(&b, `<li><img class="photo" src="%s"></li>`, u)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</article></body></html>`)
	return b.String()
}

func renderTemplateB(l Listing, base string) string {
	var b strings.Builder
	b.WriteString(`<html><body><div id="obj">`)
	fmt.Fprintf(&b, `<h2 itemprop="name">%s</h2><b data-role="price">%s ₴</b>`, html.EscapeString(l.Title), groupThousands(l.Price))
	// Пустой span вместо отсутствующего блока — второй способ "нет адреса".
	fmt.Fprintf(&b, `<span itemprop="address">%s</span>`, html.EscapeString(l.Address))
	fmt.Fprintf(&b, `<section class="text">%s</section>`, html.EscapeString(l.Description))
	if len(l.PhotoNames) > 0 {
		b.WriteString(`<div class="photos">`)
		for _, u := range l.PhotoURLs(base) {
			fmt.Fprintf(&b, `<a href="%s" data-photo>фото</a>`, u)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// groupThousands: 1250000 → "1 250 000" — как цену пишут на реальных сайтах;
// парсер обязан выкинуть разделители, а не полагаться на чистое число.
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ' ')
		}
		out = append(out, c)
	}
	return string(out)
}

// tinyPNG — 1×1 прозрачный PNG: Media-стадии есть что скачать и обработать.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}
