package inbound

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	shareddomain "buildscrape/internal/shared/domain"
	"buildscrape/internal/storage/domain"

	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20

type AdminHandler struct {
	admin domain.SourceAdminService
	token string
}

// NewAdminHandler — token пуст ⟹ admin API открыт (демо/локально; в лог
// пишется предупреждение). Непустой ⟹ каждый запрос обязан нести
// `Authorization: Bearer <token>`.
func NewAdminHandler(a domain.SourceAdminService, token string) *AdminHandler {
	if token == "" {
		slog.Warn("storage: ADMIN_TOKEN не задан — admin API открыт без аутентификации")
	}
	return &AdminHandler{admin: a, token: token}
}

func (h *AdminHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/sources", h.auth(h.createSource))               // F7
	mux.HandleFunc("PATCH /admin/sources/{id}/enabled", h.auth(h.setEnabled))   // F8
	mux.HandleFunc("POST /admin/sources/{id}/archive", h.auth(h.archiveSource)) // F9
}

// auth — bearer-токен, сравнение за константное время (subtle): обычное
// сравнение строк выдаёт длину общего префикса по времени ответа.
func (h *AdminHandler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.token != "" {
			// CutPrefix, а не TrimPrefix: TrimPrefix молча пропустил бы голый
			// токен без схемы "Bearer" (префикса нет — нечего срезать, строка
			// осталась равной токену).
			got, hasScheme := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !hasScheme || subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		next(w, r)
	}
}

// decodeJSON — строгий разбор тела: лимит размера, неизвестные поля — 400
// (опечатка "pol_interval_sec" не должна молча дать интервал по умолчанию).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id must be a UUID")
		return uuid.Nil, false
	}
	return id, true
}

type sourceDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	PollIntervalSec int    `json:"poll_interval_sec"`
	Enabled         bool   `json:"enabled"`
	Archived        bool   `json:"archived"`
}

type createSourceRequest struct {
	Name            string `json:"name"`
	BaseURL         string `json:"base_url"`
	PollIntervalSec int    `json:"poll_interval_sec"`
	Enabled         *bool  `json:"enabled"` // не указан ⟹ true
}

func (h *AdminHandler) createSource(w http.ResponseWriter, r *http.Request) {
	var req createSourceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	src, err := h.admin.CreateSource(r.Context(), shareddomain.Source{
		Name: req.Name, BaseURL: req.BaseURL, PollIntervalSec: req.PollIntervalSec, Enabled: enabled,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sourceDTO{
		ID: src.ID.String(), Name: src.Name, BaseURL: src.BaseURL,
		PollIntervalSec: src.PollIntervalSec, Enabled: src.Enabled, Archived: src.Archived,
	})
}

func (h *AdminHandler) setEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		// Без явного поля молча выключить/включить источник нельзя:
		// пустое тело {} не должно означать "false".
		writeError(w, http.StatusBadRequest, `field "enabled" is required`)
		return
	}
	if err := h.admin.SetEnabled(r.Context(), id, *req.Enabled); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// archiveSource — одностороннее: обратного эндпоинта нет и не будет (нет
// пути archived→active, MODEL-buildscrape.md §1.6); повторный вызов на уже
// заархивированном — 204, идемпотентно.
func (h *AdminHandler) archiveSource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := h.admin.Archive(r.Context(), id); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
