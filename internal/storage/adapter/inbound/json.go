package inbound

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	shareddomain "buildscrape/internal/shared/domain"
	"buildscrape/internal/storage/domain"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("storage: encode response", "err", err) // заголовок уже отправлен — сказать клиенту нечего
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// writeDomainError — единая таблица "ошибка домена → HTTP-статус". Неизвестная
// ошибка — 500 БЕЗ текста наружу (там могут быть детали БД/драйвера); полный
// текст уходит в лог.
func writeDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, shareddomain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, domain.ErrInvalidSource):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("storage: request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
