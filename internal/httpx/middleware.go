package httpx

import (
	"log/slog"
	"net/http"
	"time"
)

func Middleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic recovered", "error", rec, "path", r.URL.Path)
					http.Error(w, "internal error", http.StatusInternalServerError)
				}
			}()

			start := time.Now()
			next.ServeHTTP(w, r)
			logger.Debug("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
		})
	}
}

func HealthzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}

func ReadyzHandler(ready func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if ready == nil || ready() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}
}
