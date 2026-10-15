package inbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	shareddomain "buildscrape/internal/shared/domain"
	"buildscrape/internal/storage/domain"

	"github.com/google/uuid"
)

// --- fakes ---

type fakeQuery struct {
	items      []domain.ItemView
	listErr    error
	getErr     error
	gotFilter  domain.ItemFilter
	gotID      uuid.UUID
	listCalled bool
}

func (f *fakeQuery) List(ctx context.Context, filter domain.ItemFilter) ([]domain.ItemView, error) {
	f.listCalled = true
	f.gotFilter = filter
	return f.items, f.listErr
}

func (f *fakeQuery) Get(ctx context.Context, id uuid.UUID) (domain.ItemView, error) {
	f.gotID = id
	if f.getErr != nil {
		return domain.ItemView{}, f.getErr
	}
	return f.items[0], nil
}

type fakeAdmin struct {
	created   shareddomain.Source
	createErr error
	enabledID uuid.UUID
	enabled   *bool
	archived  uuid.UUID
	opErr     error
}

func (f *fakeAdmin) CreateSource(ctx context.Context, s shareddomain.Source) (shareddomain.Source, error) {
	f.created = s
	if f.createErr != nil {
		return shareddomain.Source{}, f.createErr
	}
	s.ID = uuid.New()
	return s, nil
}

func (f *fakeAdmin) SetEnabled(ctx context.Context, id uuid.UUID, enabled bool) error {
	f.enabledID, f.enabled = id, &enabled
	return f.opErr
}

func (f *fakeAdmin) Archive(ctx context.Context, id uuid.UUID) error {
	f.archived = id
	return f.opErr
}

func do(t *testing.T, mux *http.ServeMux, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func itemsMux(q domain.ItemQueryService) *http.ServeMux {
	mux := http.NewServeMux()
	NewHTTPHandler(q).Routes(mux)
	return mux
}

func adminMux(a domain.SourceAdminService, token string) *http.ServeMux {
	mux := http.NewServeMux()
	NewAdminHandler(a, token).Routes(mux)
	return mux
}

// --- GET /items ---

func TestListItems_ReturnsDTOsWithNullGeoForUngeocoded(t *testing.T) {
	lat, lng := 50.45, 30.52
	id1, id2 := uuid.New(), uuid.New()
	q := &fakeQuery{items: []domain.ItemView{
		{ID: id1, Title: "Дом", Price: 1000, Lat: &lat, Lng: &lng, AICategory: "house"},
		{ID: id2, Title: "Гараж", Price: 500}, // не геокодирован
	}}
	rec := do(t, itemsMux(q), "GET", "/items", "", nil)

	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 2 || resp.Items[0]["id"] != id1.String() {
		t.Fatalf("items=%v", resp.Items)
	}
	// null, а не 0: точка (0,0) на карте — не "нет данных".
	if v, present := resp.Items[1]["lat"]; !present || v != nil {
		t.Errorf("lat=%v (present=%v), want null", v, present)
	}
	if resp.Items[0]["lat"] != lat {
		t.Errorf("lat=%v, want %v", resp.Items[0]["lat"], lat)
	}
}

func TestListItems_EmptyIsEmptyArrayNotNull(t *testing.T) {
	rec := do(t, itemsMux(&fakeQuery{}), "GET", "/items", "", nil)
	if !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("body=%s — клиенту нужен [], а не null", rec.Body.String())
	}
}

func TestListItems_PassesFilters(t *testing.T) {
	q := &fakeQuery{}
	sid := uuid.New()
	do(t, itemsMux(q), "GET", "/items?source_id="+sid.String()+"&q=дом", "", nil)
	if q.gotFilter.SourceID != sid || q.gotFilter.Query != "дом" {
		t.Errorf("filter=%+v", q.gotFilter)
	}
}

func TestListItems_BadSourceIDIs400WithoutTouchingService(t *testing.T) {
	q := &fakeQuery{}
	rec := do(t, itemsMux(q), "GET", "/items?source_id=not-a-uuid", "", nil)
	if rec.Code != 400 || q.listCalled {
		t.Errorf("code=%d listCalled=%v", rec.Code, q.listCalled)
	}
}

func TestListItems_InternalErrorDoesNotLeakDetails(t *testing.T) {
	q := &fakeQuery{listErr: errors.New(`pq: password authentication failed for user "buildscrape"`)}
	rec := do(t, itemsMux(q), "GET", "/items", "", nil)
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "password") {
		t.Errorf("code=%d body=%s — детали БД не должны утекать в ответ", rec.Code, rec.Body.String())
	}
}

// --- GET /items/{id} ---

func TestGetItem_OK(t *testing.T) {
	id := uuid.New()
	rec := do(t, itemsMux(&fakeQuery{items: []domain.ItemView{{ID: id, Title: "Дом"}}}), "GET", "/items/"+id.String(), "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), id.String()) {
		t.Errorf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetItem_NotFoundIs404(t *testing.T) {
	q := &fakeQuery{getErr: fmt.Errorf("get item: %w", shareddomain.ErrNotFound)}
	rec := do(t, itemsMux(q), "GET", "/items/"+uuid.NewString(), "", nil)
	if rec.Code != 404 {
		t.Errorf("code=%d, want 404", rec.Code)
	}
}

func TestGetItem_BadIDIs400(t *testing.T) {
	if rec := do(t, itemsMux(&fakeQuery{}), "GET", "/items/xyz", "", nil); rec.Code != 400 {
		t.Errorf("code=%d, want 400", rec.Code)
	}
}

// --- POST /admin/sources ---

func TestCreateSource_201AndDefaultsEnabledTrue(t *testing.T) {
	a := &fakeAdmin{}
	rec := do(t, adminMux(a, ""), "POST", "/admin/sources", `{"name":"mock","base_url":"http://m/listings"}`, nil)

	if rec.Code != 201 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !a.created.Enabled {
		t.Error("enabled не указан ⟹ должен быть true: источник, который создали, чтобы опрашивать")
	}
	var dto sourceDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil || dto.ID == "" || dto.Name != "mock" || !dto.Enabled {
		t.Errorf("dto=%+v err=%v", dto, err)
	}
}

func TestCreateSource_ExplicitEnabledFalseIsRespected(t *testing.T) {
	a := &fakeAdmin{}
	do(t, adminMux(a, ""), "POST", "/admin/sources", `{"name":"x","base_url":"http://m","enabled":false}`, nil)
	if a.created.Enabled {
		t.Error("явное enabled=false проигнорировано")
	}
}

func TestCreateSource_ValidationErrorIs400WithReason(t *testing.T) {
	a := &fakeAdmin{createErr: fmt.Errorf("%w: name is required", domain.ErrInvalidSource)}
	rec := do(t, adminMux(a, ""), "POST", "/admin/sources", `{"base_url":"http://m"}`, nil)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "name is required") {
		t.Errorf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateSource_BadJSONAndUnknownFieldsAre400(t *testing.T) {
	for name, body := range map[string]string{
		"битый JSON":       `{"name":`,
		"неизвестное поле": `{"name":"x","base_url":"http://m","pol_interval_sec":10}`, // опечатка
		"не объект":        `[]`,
	} {
		a := &fakeAdmin{}
		rec := do(t, adminMux(a, ""), "POST", "/admin/sources", body, nil)
		if rec.Code != 400 {
			t.Errorf("%s: code=%d, want 400", name, rec.Code)
		}
	}
}

// --- PATCH /admin/sources/{id}/enabled ---

func TestSetEnabled_204(t *testing.T) {
	a := &fakeAdmin{}
	id := uuid.New()
	rec := do(t, adminMux(a, ""), "PATCH", "/admin/sources/"+id.String()+"/enabled", `{"enabled":false}`, nil)
	if rec.Code != 204 || a.enabledID != id || a.enabled == nil || *a.enabled != false {
		t.Errorf("code=%d id=%v enabled=%v", rec.Code, a.enabledID, a.enabled)
	}
}

func TestSetEnabled_MissingFieldIs400NotSilentFalse(t *testing.T) {
	a := &fakeAdmin{}
	rec := do(t, adminMux(a, ""), "PATCH", "/admin/sources/"+uuid.NewString()+"/enabled", `{}`, nil)
	if rec.Code != 400 || a.enabled != nil {
		t.Errorf("code=%d enabled=%v — пустое тело не должно молча выключать источник", rec.Code, a.enabled)
	}
}

func TestSetEnabled_UnknownSourceIs404(t *testing.T) {
	a := &fakeAdmin{opErr: fmt.Errorf("set enabled: %w", shareddomain.ErrNotFound)}
	rec := do(t, adminMux(a, ""), "PATCH", "/admin/sources/"+uuid.NewString()+"/enabled", `{"enabled":true}`, nil)
	if rec.Code != 404 {
		t.Errorf("code=%d, want 404", rec.Code)
	}
}

func TestSetEnabled_BadIDIs400(t *testing.T) {
	rec := do(t, adminMux(&fakeAdmin{}, ""), "PATCH", "/admin/sources/nope/enabled", `{"enabled":true}`, nil)
	if rec.Code != 400 {
		t.Errorf("code=%d, want 400", rec.Code)
	}
}

// --- POST /admin/sources/{id}/archive ---

func TestArchive_204AndUnknownIs404(t *testing.T) {
	a := &fakeAdmin{}
	id := uuid.New()
	if rec := do(t, adminMux(a, ""), "POST", "/admin/sources/"+id.String()+"/archive", "", nil); rec.Code != 204 || a.archived != id {
		t.Errorf("code=%d archived=%v", rec.Code, a.archived)
	}
	a.opErr = fmt.Errorf("archive: %w", shareddomain.ErrNotFound)
	if rec := do(t, adminMux(a, ""), "POST", "/admin/sources/"+uuid.NewString()+"/archive", "", nil); rec.Code != 404 {
		t.Errorf("code=%d, want 404", rec.Code)
	}
}

// Нет обратного эндпоинта: archived→active не существует (MODEL §1.6).
func TestNoUnarchiveRoute(t *testing.T) {
	mux := adminMux(&fakeAdmin{}, "")
	for _, target := range []string{"/admin/sources/" + uuid.NewString() + "/unarchive", "/admin/sources/" + uuid.NewString() + "/restore"} {
		if rec := do(t, mux, "POST", target, "", nil); rec.Code != 404 {
			t.Errorf("%s: code=%d, want 404", target, rec.Code)
		}
	}
}

// --- аутентификация ---

func TestAdminAuth_TokenRequiredWhenConfigured(t *testing.T) {
	a := &fakeAdmin{}
	mux := adminMux(a, "s3cret")
	id := uuid.NewString()

	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"без заголовка", nil, 401},
		{"неверный токен", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"токен без схемы Bearer", map[string]string{"Authorization": "s3cret"}, 401}, // верный токен, но без схемы — не принимается
		{"верный токен", map[string]string{"Authorization": "Bearer s3cret"}, 204},
	}
	for _, tc := range cases {
		rec := do(t, mux, "POST", "/admin/sources/"+id+"/archive", "", tc.hdr)
		if rec.Code != tc.want {
			t.Errorf("%s: code=%d, want %d", tc.name, rec.Code, tc.want)
		}
	}
	if rec := do(t, mux, "POST", "/admin/sources/"+id+"/archive", "", nil); rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("401 без WWW-Authenticate")
	}
}

func TestAdminAuth_RejectedRequestNeverReachesService(t *testing.T) {
	a := &fakeAdmin{}
	do(t, adminMux(a, "s3cret"), "POST", "/admin/sources", `{"name":"x","base_url":"http://m"}`, nil)
	if a.created.Name != "" {
		t.Error("неавторизованный запрос дошёл до сервиса и создал источник")
	}
}

func TestAdminAuth_ItemsEndpointsStayPublic(t *testing.T) {
	// Токен защищает ТОЛЬКО /admin/*: публичная выдача — публична.
	if rec := do(t, itemsMux(&fakeQuery{}), "GET", "/items", "", nil); rec.Code != 200 {
		t.Errorf("code=%d", rec.Code)
	}
}
