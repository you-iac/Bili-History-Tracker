// Package httpapi exposes platform-independent metadata queries over HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"bili-history-tracker/backend/internal/source"
)

type Querier interface {
	GetVideo(context.Context, string, string) (source.Video, error)
	GetCreator(context.Context, string, string) (source.CreatorProfile, error)
}

func New(queries Querier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/platforms/{platform}/videos/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		video, err := queries.GetVideo(ctx, r.PathValue("platform"), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]source.Video{"data": video})
	})
	mux.HandleFunc("GET /api/v1/platforms/{platform}/creators/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		creator, err := queries.GetCreator(ctx, r.PathValue("platform"), r.PathValue("id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]source.CreatorProfile{"data": creator})
	})
	return mux
}

func writeError(w http.ResponseWriter, err error) {
	status, kind := http.StatusBadGateway, source.UpstreamFailure
	var queryError *source.Error
	var netError net.Error
	if errors.As(err, &queryError) {
		kind = queryError.Kind
		switch kind {
		case source.InvalidInput:
			status = http.StatusBadRequest
		case source.UnsupportedPlatform, source.NotFound:
			status = http.StatusNotFound
		case source.UnsupportedOperation:
			status = http.StatusNotImplemented
		case source.RateLimited:
			status = http.StatusServiceUnavailable
		}
	} else if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netError) && netError.Timeout()) {
		status = http.StatusGatewayTimeout
	} else if errors.Is(err, context.Canceled) {
		status = http.StatusRequestTimeout
	}
	// Never send raw upstream bodies or transport error details to clients.
	writeJSON(w, status, map[string]any{"error": map[string]source.ErrorKind{"code": kind}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
