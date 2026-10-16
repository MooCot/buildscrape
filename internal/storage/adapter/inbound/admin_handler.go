package inbound

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	shareddomain "buildscrape/internal/shared/domain"
	"buildscrape/internal/storage/domain"

	"github.com/google/uuid"
)

const maxBodyBytes = 1 << 20

type AdminHandler struct {
	admin domain.SourceAdminService
	auth  domain.ApiKeyAuthenticator
}

// NewAdminHandler — RBAC через domain.ApiKeyAuthenticator (rbac.go):
// заменяет единственный статичный ADMIN_TOKEN (CLAUDE.md "Не сделано" —
// "единственная защита, без ролей и ротации"). Выдача/ротация ключей —
// CLI `buildscrape admin create-key`/`revoke-key` (cmd/buildscrape/admin.go),
// не HTTP: нет вообще ни одного shared-секрета, который нужно было бы
// передавать этому хендлеру напрямую.
func NewAdminHandler(a domain.SourceAdminService, authSvc domain.ApiKeyAuthenticator) *AdminHandler {
	return &AdminHandler{admin: a, auth: authSvc}
}

func (h *AdminHandler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/sources", h.requirePermission(domain.PermSourcesWrite, h.createSource))               // F7
	mux.HandleFunc("PATCH /admin/sources/{id}/enabled", h.requirePermission(domain.PermSourcesToggle, h.setEnabled))   // F8
	mux.HandleFunc("POST /admin/sources/{id}/archive", h.requirePermission(domain.PermSourcesArchive, h.archiveSource)) // F9
}

// requirePermission — два разных отказа, не один "unauthorized" на всё:
// 401 (токен отсутствует/неизвестен/отозван — ErrUnauthenticated,
// неразличимо по rbac.go) vs 403 (токен валиден, но у его ролей нет
// именно этого права). Раньше (ADMIN_TOKEN) различия не было вообще —
// один секрет открывал все три admin-действия одинаково.
func (h *AdminHandler) requirePermission(perm domain.Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, hasScheme := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !hasScheme {
			raw = ""
		}

		identity, err := h.auth.Authenticate(r.Context(), raw)
		if err != nil {
			if errors.Is(err, domain.ErrUnauthenticated) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			writeError(w, http.StatusInternalServerError, "authentication failed")
			return
		}
		if !identity.Has(perm) {
			writeError(w, http.StatusForbidden, "forbidden: missing permission "+string(perm))
			return
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
