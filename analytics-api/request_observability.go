package main

import (
	"context"
	"log"
	"net/http"
	"regexp"
	"time"
)

type consoleRequestIDKey struct{}

var consoleRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)

func consoleRequestID(ctx context.Context) string {
	id, _ := ctx.Value(consoleRequestIDKey{}).(string)
	return id
}

type observedResponse struct {
	http.ResponseWriter
	status, bytes int
}

func (w *observedResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *observedResponse) WriteHeader(status int) {
	if status >= 200 {
		if w.status != 0 {
			return
		}
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}
func (w *observedResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Log route templates and generated IDs only: never queries, credentials,
// account IDs, bodies, or raw driver errors. Correlation survives client abort.
func observeConsoleRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Console-Request-ID")
		if !consoleRequestIDPattern.MatchString(id) {
			id = randomID()
		}
		r = r.WithContext(context.WithValue(r.Context(), consoleRequestIDKey{}, id))
		w.Header().Set("X-Console-Request-ID", id)
		out := &observedResponse{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(out, r)
		elapsed := time.Since(start)
		if elapsed < time.Second && out.status < 400 && r.Context().Err() == nil {
			return
		}
		pattern := r.Pattern
		if pattern == "" {
			pattern = "unmatched"
		}
		log.Printf("console_request request_id=%s route=%q status=%d elapsed_ms=%d response_bytes=%d canceled=%t", id, pattern, out.status, elapsed.Milliseconds(), out.bytes, r.Context().Err() != nil)
	})
}
