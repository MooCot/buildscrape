package domain

import (
	"context"
	"errors"
	"fmt"
	"time"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

// ErrGone — страница объявления исчезла (404/410): не ошибка обработки, а
// нормальное состояние источника (объявление сняли). FetchOne такое задание
// молча отбрасывает (ack), не ретраит и в DLQ не шлёт.
var ErrGone = errors.New("listing gone")

const (
	// defaultProxyBan — на сколько выключать прокси, если источник не
	// сказал сам (Retry-After) или он упал по сети.
	defaultProxyBan = time.Minute

	// defaultMaxIndexPages — предохранитель от бесконечной пагинации
	// (источник, чей "next" ведёт по кругу): 20 страниц × 50 объявлений.
	defaultMaxIndexPages = 20
)

// Service — inbound-порт Fetch. Две роли одного хексагона:
//   - ScheduleDue (F1a) вызывает таймер: найти источники к опросу и разложить
//     их объявления по fetch.queue;
//   - FetchOne (F1b) вызывает consumer fetch.queue: скачать одну страницу.
//
// Разделены, потому что масштабируется именно второе (--scale fetch-worker=N
// ускоряет разбор fetch.queue, CLAUDE.md §4.1), а первое обязано быть
// дешёвым и идемпотентным.
type Service interface {
	ScheduleDue(ctx context.Context) error
	FetchOne(ctx context.Context, sourceID uuid.UUID, url, externalID string) error
}

// Deps — зависимости Fetch (много, поэтому структурой, а не позиционными
// аргументами: перепутать пять интерфейсов подряд компилятор не заметил бы).
type Deps struct {
	Proxies   ProxyProvider
	Pages     PageFetcher
	Index     IndexExtractor
	Scheduler SourceScheduler
	Jobs      JobPublisher
	Writer    RawItemWriter
	Next      NextStagePublisher
	Sources   shareddomain.SourceRepository
}

type service struct {
	Deps
	now           func() time.Time
	maxIndexPages int
}

func NewService(d Deps) Service {
	return &service{Deps: d, now: time.Now, maxIndexPages: defaultMaxIndexPages}
}

// ScheduleDue — один тик планировщика. Ошибка по одному источнику не
// останавливает остальные (иначе один сломанный источник кормил бы всех
// голодом); все ошибки возвращаются вместе.
//
// Источник забирается ClaimDue ДО опроса: если индекс не скачался, срок уже
// сдвинут и источник ждёт следующего интервала, а не молотит по кругу.
// Осознанный компромисс: сбой индекса стоит одного интервала опроса.
func (s *service) ScheduleDue(ctx context.Context) error {
	due, err := s.Scheduler.ClaimDue(ctx, s.now())
	if err != nil {
		return fmt.Errorf("claim due sources: %w", err)
	}
	var errs []error
	for _, src := range due {
		if err := s.scheduleSource(ctx, src); err != nil {
			errs = append(errs, fmt.Errorf("source %s (%s): %w", src.ID, src.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (s *service) scheduleSource(ctx context.Context, src shareddomain.Source) error {
	// P1 (MODEL §7): F1 сам консультирует фазу источника, а не полагается на
	// то, что планировщик отфильтровал (см. SourceScheduler doc-comment).
	phase, err := s.Sources.Phase(ctx, src.ID)
	if err != nil {
		return fmt.Errorf("phase: %w", err)
	}
	if phase != shareddomain.SourcePhaseActive {
		return nil
	}

	seen := make(map[string]bool)
	url := src.BaseURL
	for page := 0; url != "" && page < s.maxIndexPages; page++ {
		body, err := s.get(ctx, url)
		if err != nil {
			return fmt.Errorf("index %s: %w", url, err)
		}
		idx, err := s.Index.ExtractIndex(body, url)
		if err != nil {
			return fmt.Errorf("%w: index %s: %v", shareddomain.ErrPermanent, url, err)
		}
		for _, l := range idx.Listings {
			if seen[l.URL] {
				continue // одно объявление на нескольких страницах индекса — одно задание
			}
			seen[l.URL] = true
			if err := s.Jobs.PublishFetchJob(ctx, src.ID, l.URL, l.ExternalID); err != nil {
				return fmt.Errorf("publish fetch job %s: %w", l.URL, err)
			}
		}
		url = idx.Next
	}
	return nil
}

// FetchOne — скачать одну страницу, сохранить RawItem, отдать Parse.
//
// Классификация ошибок — то, ради чего это отдельный метод, а не "GET и
// молиться": временное (сеть, 429, 5xx) возвращается как есть → consumer
// уводит сообщение в backoff; постоянное (прочие 4xx) оборачивается
// ErrPermanent → сразу в DLQ; исчезнувшее (404/410) → nil, задание снято.
func (s *service) FetchOne(ctx context.Context, sourceID uuid.UUID, url, externalID string) error {
	phase, err := s.Sources.Phase(ctx, sourceID)
	if err != nil {
		return fmt.Errorf("phase: %w", err)
	}
	if phase != shareddomain.SourcePhaseActive {
		// Источник выключили/архивировали между планированием и загрузкой.
		// Задание отбрасывается (ack), не ретраится: повтор дал бы тот же ответ.
		return nil
	}

	body, err := s.get(ctx, url)
	if errors.Is(err, ErrGone) {
		return nil
	}
	if err != nil {
		return err
	}

	raw := RawItem{
		ID:         uuid.New(), // item_id рождается здесь и идёт сквозным ключом (CLAUDE.md §4.4)
		SourceID:   sourceID,
		ExternalID: externalID,
		RawHTML:    body,
		FetchedAt:  s.now(),
		Status:     RawItemFetched,
	}
	// Публикация — ПОСЛЕ успешной записи, не до: иначе Parse получил бы job
	// на RawItem, которого нет, если процесс упал между шагами.
	if err := s.Writer.Save(ctx, raw); err != nil {
		return fmt.Errorf("save raw item: %w", err)
	}
	if err := s.Next.PublishParseJob(ctx, raw.ID, raw.SourceID, raw.ExternalID, raw.RawHTML); err != nil {
		return fmt.Errorf("publish parse job: %w", err)
	}
	return nil
}

// get — один запрос через прокси с классификацией ответа.
func (s *service) get(ctx context.Context, url string) (string, error) {
	proxy, err := s.Proxies.Acquire(ctx)
	if err != nil {
		return "", fmt.Errorf("acquire proxy: %w", err) // временное: прокси оживут
	}
	page, err := s.Pages.Get(ctx, proxy, url)
	if err != nil {
		s.Proxies.MarkDead(ctx, proxy, defaultProxyBan) // сеть упала — подозреваем прокси
		return "", fmt.Errorf("get %s: %w", url, err)
	}

	switch {
	case page.Status == 200:
		return page.Body, nil
	case page.Status == 429:
		// Лимит навешан на этот прокси/IP — другой прокси может пройти.
		ban := page.RetryAfter
		if ban <= 0 {
			ban = defaultProxyBan
		}
		s.Proxies.MarkDead(ctx, proxy, ban)
		return "", fmt.Errorf("get %s: status 429 (rate limited)", url)
	case page.Status >= 500:
		// Падает источник, а не прокси — прокси не трогаем.
		return "", fmt.Errorf("get %s: status %d", url, page.Status)
	case page.Status == 404 || page.Status == 410:
		return "", ErrGone
	default:
		return "", fmt.Errorf("%w: get %s: status %d", shareddomain.ErrPermanent, url, page.Status)
	}
}
