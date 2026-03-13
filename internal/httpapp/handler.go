package httpapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Data  any          `json:"data,omitempty"`
	Error *apiError    `json:"error,omitempty"`
	Meta  responseMeta `json:"meta"`
}

type responseMeta struct {
	RequestID string `json:"request_id"`
}

type apiError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
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
	requestTimeout = 15 * time.Second
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
	return NewHandlerWithRepositories(newMemoryTaskRepository(), newMemoryShortURLRepository())
}

func NewHandlerWithTaskRepository(repo taskRepository) http.Handler {
	return NewHandlerWithRepositories(repo, newMemoryShortURLRepository())
}

func NewHandlerWithRepositories(taskRepo taskRepository, shortRepo shortURLRepository) http.Handler {
	api := newTaskAPIWithRepository(taskRepo)
	shortAPI := newShortURLAPI(shortRepo)
	authAPI := newAuthAPI()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", helloHandler)
	mux.HandleFunc("GET /health", healthHandler)
	mux.Handle("GET /tasks", authAPI.authMiddleware(http.HandlerFunc(api.listTasks)))
	mux.Handle("POST /tasks", authAPI.authMiddleware(http.HandlerFunc(api.createTask)))
	mux.Handle("GET /tasks/{id}", authAPI.authMiddleware(http.HandlerFunc(api.getTask)))
	mux.Handle("PUT /tasks/{id}", authAPI.authMiddleware(http.HandlerFunc(api.updateTask)))
	mux.Handle("DELETE /tasks/{id}", authAPI.authMiddleware(http.HandlerFunc(api.deleteTask)))
	mux.HandleFunc("POST /short-urls", shortAPI.createShortURL)
	mux.HandleFunc("GET /short-urls/{code}", shortAPI.getShortURL)
	mux.HandleFunc("POST /auth/signup", authAPI.signup)
	mux.HandleFunc("POST /auth/login", authAPI.login)
	mux.HandleFunc("POST /auth/refresh", authAPI.refresh)
	mux.Handle("GET /auth/me", authAPI.authMiddleware(http.HandlerFunc(authAPI.me)))
	mux.HandleFunc("GET /{code}", shortAPI.redirectByCode)

	return withRequestLogging(withRequestTimeout(withRequestID(mux), requestTimeout))
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

func respondErrorJSON(w http.ResponseWriter, r *http.Request, status int, errResp apiError) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	reqID, _ := requestIDFromContext(r.Context())
	payload := responseEnvelope{
		Error: &errResp,
		Meta:  responseMeta{RequestID: reqID},
	}

	if err := jsonEncodeFn(w, payload); err != nil {
		loggerPrintfFn("httpapp error response encode error path=%s err=%v", r.URL.Path, err)
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

func withRequestTimeout(next http.Handler, timeout time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if timeout <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFromContext(ctx context.Context) (string, bool) {
	value, ok := ctx.Value(requestIDContextKey).(string)
	return value, ok
}

func decodeJSONBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain a single JSON object")
		}
		return errors.New("request body must contain a single JSON object")
	}
	return nil
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
