package httpapi

import (
	"log/slog"
	"net/http"
)

// apiError is the common error envelope from docs/api.md
// ("Общий формат ошибки").
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type apiErrorBody struct {
	Error apiError `json:"error"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiErrorBody{Error: apiError{Code: code, Message: message}})
}

// writeInternal logs the real error server-side and returns a generic 500 body,
// so internal details (SQL, filesystem paths, upstream URLs) never reach the
// client. Use instead of putting err.Error() in the response message.
func writeInternal(w http.ResponseWriter, err error) {
	slog.Default().Error("http internal error", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "внутрішня помилка")
}

func writeBadRequest(w http.ResponseWriter, message string) {
	writeError(w, http.StatusBadRequest, "bad_request", message)
}

func writeUpstreamUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusBadGateway, "upstream_unavailable", "TMDB недоступний і кешу немає")
}

func writeSourcesUnavailable(w http.ResponseWriter) {
	writeError(w, http.StatusBadGateway, "upstream_unavailable", "джерело недоступне")
}

func writeNotFound(w http.ResponseWriter, code, message string) {
	writeError(w, http.StatusNotFound, code, message)
}
