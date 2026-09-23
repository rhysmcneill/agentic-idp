package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// errorResponse is the structured shape returned to clients for every
// non-2xx response — a stable code plus a human message, never a raw Go
// error string, which would leak internal detail and give a caller nothing
// stable to branch on.
type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	codeInvalidRequest = "invalid_request"
	codeUnauthorized   = "unauthorized"
	codeForbidden      = "forbidden"
	codeNotFound       = "not_found"
	codeConflict       = "conflict"
	codeGone           = "gone"
	codeInternal       = "internal"
)

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{Code: code, Message: message})
}

// writeInternalError logs the real error (never sent to the client) and
// writes a generic response in its place.
func writeInternalError(w http.ResponseWriter, err error) {
	slog.Error("internal error", "error", err)
	writeError(w, http.StatusInternalServerError, codeInternal, "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
