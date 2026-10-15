package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	shareddomain "buildscrape/internal/shared/domain"

	"github.com/google/uuid"
)

type fakeSourceRepo struct {
	created []shareddomain.Source
	err     error
}

func (r *fakeSourceRepo) Create(ctx context.Context, s shareddomain.Source) (shareddomain.Source, error) {
	if r.err != nil {
		return shareddomain.Source{}, r.err
	}
	r.created = append(r.created, s)
	return s, nil
}
func (r *fakeSourceRepo) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	return r.err
}
func (r *fakeSourceRepo) Archive(ctx context.Context, id uuid.UUID) error { return r.err }
func (r *fakeSourceRepo) Phase(ctx context.Context, id uuid.UUID) (shareddomain.SourcePhase, error) {
	return shareddomain.SourcePhaseActive, nil
}

func create(t *testing.T, src shareddomain.Source) (shareddomain.Source, *fakeSourceRepo, error) {
	t.Helper()
	repo := &fakeSourceRepo{}
	got, err := NewSourceAdminService(repo).CreateSource(context.Background(), src)
	return got, repo, err
}

func TestCreateSource_AssignsIdentityAndDefaults(t *testing.T) {
	got, repo, err := create(t, shareddomain.Source{Name: "  mock  ", BaseURL: "http://m:9090/listings", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == uuid.Nil {
		t.Error("ID не сгенерирован — домен владеет идентичностью, а не DEFAULT в схеме")
	}
	if got.Name != "mock" {
		t.Errorf("Name=%q, want обрезанное", got.Name)
	}
	if got.PollIntervalSec != defaultPollIntervalSec {
		t.Errorf("PollIntervalSec=%d, want %d по умолчанию", got.PollIntervalSec, defaultPollIntervalSec)
	}
	if len(repo.created) != 1 || repo.created[0].ID != got.ID {
		t.Error("в репозиторий ушёл не тот источник, что вернулся")
	}
}

func TestCreateSource_NeverStartsArchived(t *testing.T) {
	// archived — одностороннее состояние; начинать жизнь с него бессмысленно.
	got, _, err := create(t, shareddomain.Source{Name: "x", BaseURL: "http://m", Archived: true})
	if err != nil || got.Archived {
		t.Errorf("archived=%v err=%v", got.Archived, err)
	}
}

func TestCreateSource_IgnoresClientSuppliedID(t *testing.T) {
	forged := uuid.New()
	got, _, _ := create(t, shareddomain.Source{ID: forged, Name: "x", BaseURL: "http://m"})
	if got.ID == forged {
		t.Error("id пришёл снаружи и был принят — клиент не выбирает идентичность")
	}
}

func TestCreateSource_Validation(t *testing.T) {
	cases := []struct {
		name string
		src  shareddomain.Source
		want string
	}{
		{"пустое имя", shareddomain.Source{Name: "  ", BaseURL: "http://m"}, "name"},
		{"пустой url", shareddomain.Source{Name: "x", BaseURL: ""}, "base_url"},
		{"не http", shareddomain.Source{Name: "x", BaseURL: "ftp://m/listings"}, "base_url"},
		{"относительный url", shareddomain.Source{Name: "x", BaseURL: "/listings"}, "base_url"},
		{"без хоста", shareddomain.Source{Name: "x", BaseURL: "http://"}, "base_url"},
		{"слишком частый опрос", shareddomain.Source{Name: "x", BaseURL: "http://m", PollIntervalSec: 1}, "poll_interval_sec"},
		{"отрицательный интервал", shareddomain.Source{Name: "x", BaseURL: "http://m", PollIntervalSec: -5}, "poll_interval_sec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, repo, err := create(t, tc.src)
			if !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("err=%v, want ErrInvalidSource", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err=%q не называет поле %q", err, tc.want)
			}
			if len(repo.created) != 0 {
				t.Error("невалидный источник дошёл до репозитория")
			}
		})
	}
}

func TestCreateSource_AcceptsMinimumInterval(t *testing.T) {
	if _, _, err := create(t, shareddomain.Source{Name: "x", BaseURL: "https://m", PollIntervalSec: minPollIntervalSec}); err != nil {
		t.Errorf("граничное значение должно приниматься: %v", err)
	}
}

func TestCreateSource_RepoErrorPropagates(t *testing.T) {
	repo := &fakeSourceRepo{err: errors.New("db down")}
	if _, err := NewSourceAdminService(repo).CreateSource(context.Background(), shareddomain.Source{Name: "x", BaseURL: "http://m"}); err == nil || errors.Is(err, ErrInvalidSource) {
		t.Errorf("err=%v — сбой БД не должен маскироваться под 400", err)
	}
}

// --- ItemQueryService: тонкая делегация, но контракт "ошибка проходит как есть" важен для 404 ---

type fakeReader struct {
	getErr error
}

func (r *fakeReader) List(ctx context.Context, f ItemFilter) ([]ItemView, error) { return nil, nil }
func (r *fakeReader) Get(ctx context.Context, id uuid.UUID) (ItemView, error) {
	return ItemView{}, r.getErr
}

func TestGet_NotFoundPassesThroughIntact(t *testing.T) {
	r := &fakeReader{getErr: shareddomain.ErrNotFound}
	_, err := NewItemQueryService(r).Get(context.Background(), uuid.New())
	if !errors.Is(err, shareddomain.ErrNotFound) {
		t.Errorf("err=%v — сервис не должен терять сентинел (HTTP-слой по нему выбирает 404)", err)
	}
}
