// Package inbound (storage) — REST API (F6) + admin-эндпоинты (F7/F8/F9).
package inbound

import (
	"net/http"

	"buildscrape/internal/storage/domain"

	"github.com/google/uuid"
)

type HTTPHandler struct {
	query domain.ItemQueryService
}

func NewHTTPHandler(q domain.ItemQueryService) *HTTPHandler {
	return &HTTPHandler{query: q}
}

func (h *HTTPHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /items", h.listItems)
	mux.HandleFunc("GET /items/{id}", h.getItem)
}

// itemDTO — wire-формат ответа. Отдельный тип, а не json-теги на
// domain.ItemView: домен про JSON не знает (тот же принцип, что у
// shared/contracts — перевод в wire-формат делает адаптер).
type itemDTO struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Price       float64  `json:"price"`
	Lat         *float64 `json:"lat"` // null — не геокодирован
	Lng         *float64 `json:"lng"`
	AICategory  string   `json:"ai_category,omitempty"`
}

func toDTO(v domain.ItemView) itemDTO {
	return itemDTO{
		ID: v.ID.String(), Title: v.Title, Description: v.Description, Price: v.Price,
		Lat: v.Lat, Lng: v.Lng, AICategory: v.AICategory,
	}
}

func (h *HTTPHandler) listItems(w http.ResponseWriter, r *http.Request) {
	filter := domain.ItemFilter{Query: r.URL.Query().Get("q")}
	if raw := r.URL.Query().Get("source_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "source_id must be a UUID")
			return
		}
		filter.SourceID = id
	}

	items, err := h.query.List(r.Context(), filter)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]itemDTO, 0, len(items)) // [] а не null для пустой выдачи
	for _, v := range items {
		out = append(out, toDTO(v))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *HTTPHandler) getItem(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return
	}
	v, err := h.query.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDTO(v))
}
