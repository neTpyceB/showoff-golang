package httpapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTaskHandlersRepositoryInternalErrors(t *testing.T) {
	restoreGlobals(t)

	var logs []string
	loggerPrintfFn = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	newRequestIDFn = func() string { return "req-internal" }

	repo := failingTaskRepository{err: errors.New("db failed")}
	h := NewHandlerWithTaskRepository(repo)
	token := authTokenForRole(t, h, "user")

	t.Run("list tasks internal error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", rec.Code)
		}
		assertAPIErrorCode(t, rec, "internal_error")
	})

	t.Run("create task internal error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"title":"x","note":"","done":false}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", rec.Code)
		}
		assertAPIErrorCode(t, rec, "internal_error")
	})

	t.Run("repository error mapper direct", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks/1", nil)
		req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey, "req-direct"))
		respondTaskRepositoryError(rec, req, errors.New("boom"))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", rec.Code)
		}
		assertAPIErrorCode(t, rec, "internal_error")
	})

	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "task repository error") {
		t.Fatalf("expected repository error logs, got %s", joined)
	}
}

func TestNewTaskAPIConvenience(t *testing.T) {
	if api := newTaskAPI(); api == nil || api.repo == nil {
		t.Fatalf("newTaskAPI() = %#v", api)
	}
}

type failingTaskRepository struct {
	err error
}

func (f failingTaskRepository) List(context.Context) ([]task, error) { return nil, f.err }
func (f failingTaskRepository) Create(context.Context, taskInput, time.Time) (task, error) {
	return task{}, f.err
}
func (f failingTaskRepository) Get(context.Context, int64) (task, error) { return task{}, f.err }
func (f failingTaskRepository) Update(context.Context, int64, taskInput, time.Time) (task, error) {
	return task{}, f.err
}
func (f failingTaskRepository) Delete(context.Context, int64) error { return f.err }
