package httpapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"showoff-golang/internal/hello"
)

const serviceName = "showoff-golang"

type contextKey string

const requestIDContextKey contextKey = "request_id"

type responseEnvelope struct {
	Data any          `json:"data"`
	Meta responseMeta `json:"meta"`
}

type responseMeta struct {
	RequestID string `json:"request_id"`
}

type helloResponse struct {
	Message string `json:"message"`
}

type healthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

var (
	nowFn          = time.Now
	loggerPrintfFn = log.Printf
	requestSeq     uint64
	newRequestIDFn = func() string {
		id := atomic.AddUint64(&requestSeq, 1)
		return fmt.Sprintf("req-%06d", id)
	}
	jsonEncodeFn = func(w http.ResponseWriter, v any) error {
		return json.NewEncoder(w).Encode(v)
	}
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", helloHandler)
	mux.HandleFunc("GET /health", healthHandler)

	return withRequestLogging(withRequestID(mux))
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, r, http.StatusOK, helloResponse{
		Message: hello.Message(),
	})
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, r, http.StatusOK, healthResponse{
		Status:    "ok",
		Service:   serviceName,
		Timestamp: nowFn().UTC().Format(time.RFC3339),
	})
}

func respondJSON(w http.ResponseWriter, r *http.Request, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	reqID, _ := requestIDFromContext(r.Context())
	payload := responseEnvelope{
		Data: data,
		Meta: responseMeta{RequestID: reqID},
	}

	if err := jsonEncodeFn(w, payload); err != nil {
		loggerPrintfFn("httpapp response encode error path=%s err=%v", r.URL.Path, err)
	}
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := newRequestIDFn()
		w.Header().Set("X-Request-ID", reqID)

		ctx := context.WithValue(r.Context(), requestIDContextKey, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func withRequestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := nowFn()
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		durationMs := time.Since(start).Milliseconds()
		reqID, _ := requestIDFromContext(r.Context())

		loggerPrintfFn(
			"http_request method=%s path=%s status=%d duration_ms=%d request_id=%s",
			r.Method,
			r.URL.Path,
			rec.StatusCode(),
			durationMs,
			reqID,
		)
	})
}

func requestIDFromContext(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(requestIDContextKey).(string)
	return value, ok
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *statusRecorder) StatusCode() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
