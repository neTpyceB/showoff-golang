package httpapp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTasksCRUDFlowAndErrors(t *testing.T) {
	restoreGlobals(t)

	nowFn = func() time.Time {
		return time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)
	}

	reqCounter := 0
	newRequestIDFn = func() string {
		reqCounter++
		return fmt.Sprintf("req-%d", reqCounter)
	}

	var logs []string
	loggerPrintfFn = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	h := NewHandler()

	t.Run("list empty", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/tasks", "")
		assertStatus(t, rec, http.StatusOK)
		assertHeaderPresent(t, rec, "X-Request-ID")

		var body struct {
			Data struct {
				Tasks []taskResponse `json:"tasks"`
			} `json:"data"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		if len(body.Data.Tasks) != 0 {
			t.Fatalf("tasks len = %d, want 0", len(body.Data.Tasks))
		}
	})

	var createdID int64

	t.Run("create validation error", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/tasks", `{"title":"  "}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "validation_error")
	})

	t.Run("create invalid json unknown field", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/tasks", `{"title":"x","unknown":1}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "invalid_json")
	})

	t.Run("create success", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPost, "/tasks", `{"title":"First task","note":"hello","done":false}`)
		assertStatus(t, rec, http.StatusCreated)

		var body struct {
			Data struct {
				Task taskResponse `json:"task"`
			} `json:"data"`
			Meta struct {
				RequestID string `json:"request_id"`
			} `json:"meta"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		createdID = body.Data.Task.ID
		if createdID != 1 {
			t.Fatalf("created id = %d, want 1", createdID)
		}
		if body.Data.Task.CreatedAt != "2026-02-26T12:00:00Z" || body.Data.Task.UpdatedAt != "2026-02-26T12:00:00Z" {
			t.Fatalf("timestamps = %+v", body.Data.Task)
		}
	})

	t.Run("get invalid id", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/tasks/abc", "")
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "invalid_id")
	})

	t.Run("get not found", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, "/tasks/999", "")
		assertStatus(t, rec, http.StatusNotFound)
		assertAPIErrorCode(t, rec, "not_found")
	})

	t.Run("get success", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, fmt.Sprintf("/tasks/%d", createdID), "")
		assertStatus(t, rec, http.StatusOK)

		var body struct {
			Data struct {
				Task taskResponse `json:"task"`
			} `json:"data"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		if body.Data.Task.Title != "First task" || body.Data.Task.Note != "hello" || body.Data.Task.Done {
			t.Fatalf("task = %+v", body.Data.Task)
		}
	})

	t.Run("update invalid json multiple objects", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPut, fmt.Sprintf("/tasks/%d", createdID), `{"title":"x"}{"title":"y"}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "invalid_json")
	})

	t.Run("update invalid id", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPut, "/tasks/nope", `{"title":"x","note":"","done":false}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "invalid_id")
	})

	t.Run("update validation error", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPut, fmt.Sprintf("/tasks/%d", createdID), `{"title":"","note":"x"}`)
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "validation_error")
	})

	t.Run("update not found", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPut, "/tasks/999", `{"title":"x","note":"","done":false}`)
		assertStatus(t, rec, http.StatusNotFound)
		assertAPIErrorCode(t, rec, "not_found")
	})

	t.Run("update success", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodPut, fmt.Sprintf("/tasks/%d", createdID), `{"title":"Updated task","note":"updated","done":true}`)
		assertStatus(t, rec, http.StatusOK)

		var body struct {
			Data struct {
				Task taskResponse `json:"task"`
			} `json:"data"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		if body.Data.Task.Title != "Updated task" || body.Data.Task.Note != "updated" || !body.Data.Task.Done {
			t.Fatalf("updated task = %+v", body.Data.Task)
		}
		if body.Data.Task.CreatedAt != "2026-02-26T12:00:00Z" {
			t.Fatalf("created_at changed: %+v", body.Data.Task)
		}
		if body.Data.Task.UpdatedAt != "2026-02-26T12:00:00Z" {
			t.Fatalf("updated_at = %q", body.Data.Task.UpdatedAt)
		}
	})

	t.Run("list returns tasks sorted", func(t *testing.T) {
		_ = doRequest(t, h, http.MethodPost, "/tasks", `{"title":"Second task","note":"","done":false}`)
		rec := doRequest(t, h, http.MethodGet, "/tasks", "")
		assertStatus(t, rec, http.StatusOK)

		var body struct {
			Data struct {
				Tasks []taskResponse `json:"tasks"`
			} `json:"data"`
		}
		decodeJSON(t, rec.Body.Bytes(), &body)
		if len(body.Data.Tasks) != 2 {
			t.Fatalf("tasks len = %d", len(body.Data.Tasks))
		}
		if body.Data.Tasks[0].ID != 1 || body.Data.Tasks[1].ID != 2 {
			t.Fatalf("task order = %+v", body.Data.Tasks)
		}
	})

	t.Run("delete invalid id zero", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodDelete, "/tasks/0", "")
		assertStatus(t, rec, http.StatusBadRequest)
		assertAPIErrorCode(t, rec, "invalid_id")
	})

	t.Run("delete not found", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodDelete, "/tasks/999", "")
		assertStatus(t, rec, http.StatusNotFound)
		assertAPIErrorCode(t, rec, "not_found")
	})

	t.Run("delete success", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodDelete, fmt.Sprintf("/tasks/%d", createdID), "")
		assertStatus(t, rec, http.StatusNoContent)
		if rec.Body.Len() != 0 {
			t.Fatalf("expected empty body, got %q", rec.Body.String())
		}
	})

	t.Run("deleted task is gone", func(t *testing.T) {
		rec := doRequest(t, h, http.MethodGet, fmt.Sprintf("/tasks/%d", createdID), "")
		assertStatus(t, rec, http.StatusNotFound)
	})

	if joined := strings.Join(logs, "\n"); !strings.Contains(joined, "path=/tasks") {
		t.Fatalf("expected task request logs, got %s", joined)
	}
}

func TestTaskHelpersAndStoreBranches(t *testing.T) {
	t.Run("validateTaskInput length limits", func(t *testing.T) {
		fields := validateTaskInput(taskInput{
			Title: strings.Repeat("x", 201),
			Note:  strings.Repeat("y", 2001),
		})
		if fields["title"] == "" || fields["note"] == "" {
			t.Fatalf("expected title and note errors, got %#v", fields)
		}
	})

	t.Run("store get update delete not found", func(t *testing.T) {
		s := &memoryTaskRepository{nextID: 1, tasks: map[int64]task{}}
		if _, err := s.Get(context.Background(), 1); !errors.Is(err, errTaskNotFound) {
			t.Fatalf("get err = %v", err)
		}
		if _, err := s.Update(context.Background(), 1, taskInput{Title: "x"}, time.Now()); !errors.Is(err, errTaskNotFound) {
			t.Fatalf("update err = %v", err)
		}
		if err := s.Delete(context.Background(), 1); !errors.Is(err, errTaskNotFound) {
			t.Fatalf("delete err = %v", err)
		}
	})

	t.Run("store create update list success", func(t *testing.T) {
		s := &memoryTaskRepository{nextID: 1, tasks: map[int64]task{}}
		t1, _ := s.Create(context.Background(), taskInput{Title: "b"}, time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC))
		t2, _ := s.Create(context.Background(), taskInput{Title: "a"}, time.Date(2026, 2, 26, 10, 1, 0, 0, time.UTC))
		if t1.ID != 1 || t2.ID != 2 {
			t.Fatalf("ids = %d,%d", t1.ID, t2.ID)
		}
		updated, err := s.Update(context.Background(), 1, taskInput{Title: "bb", Note: "n", Done: true}, time.Date(2026, 2, 26, 10, 5, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("update error: %v", err)
		}
		if updated.CreatedAt.Format(time.RFC3339) != "2026-02-26T10:00:00Z" || updated.UpdatedAt.Format(time.RFC3339) != "2026-02-26T10:05:00Z" {
			t.Fatalf("updated timestamps = %+v", updated)
		}
		items, err := s.List(context.Background())
		if err != nil {
			t.Fatalf("list error: %v", err)
		}
		if len(items) != 2 || items[0].ID != 1 || items[1].ID != 2 {
			t.Fatalf("list order = %+v", items)
		}
	})

	t.Run("compareTaskByID equality", func(t *testing.T) {
		if got := compareTaskByID(task{ID: 1}, task{ID: 2}); got >= 0 {
			t.Fatalf("compareTaskByID lt = %d, want < 0", got)
		}
		if got := compareTaskByID(task{ID: 2}, task{ID: 1}); got <= 0 {
			t.Fatalf("compareTaskByID gt = %d, want > 0", got)
		}
		if got := compareTaskByID(task{ID: 1}, task{ID: 1}); got != 0 {
			t.Fatalf("compareTaskByID equality = %d, want 0", got)
		}
	})

	t.Run("parseTaskID helper invalid", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/tasks/abc", nil)
		req.SetPathValue("id", "abc")
		rec := httptest.NewRecorder()
		req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey, "req-1"))
		if _, ok := parseTaskID(rec, req); ok {
			t.Fatal("expected parseTaskID failure")
		}
	})
}

func TestDecodeJSONBodyBranches(t *testing.T) {
	t.Run("single object ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"title":"x"}`))
		var dst map[string]any
		if err := decodeJSONBody(req, &dst); err != nil {
			t.Fatalf("decodeJSONBody error: %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"title":`))
		var dst map[string]any
		if err := decodeJSONBody(req, &dst); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("extra json value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"title":"x"}{"y":1}`))
		var dst map[string]any
		if err := decodeJSONBody(req, &dst); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("extra json null token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"title":"x"} null`))
		var dst map[string]any
		if err := decodeJSONBody(req, &dst); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestRespondErrorJSONEncodeErrorIsLogged(t *testing.T) {
	restoreGlobals(t)

	var logs []string
	loggerPrintfFn = func(format string, args ...any) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}
	jsonEncodeFn = func(http.ResponseWriter, any) error {
		return errors.New("encode failed")
	}

	req := httptest.NewRequest(http.MethodGet, "/tasks/1", nil)
	req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey, "req-e"))
	rec := httptest.NewRecorder()

	respondErrorJSON(rec, req, http.StatusBadRequest, apiError{Code: "x", Message: "y"})

	assertStatus(t, rec, http.StatusBadRequest)
	if len(logs) != 1 || !strings.Contains(logs[0], "error response encode error") {
		t.Fatalf("logs = %#v", logs)
	}
}

func doRequest(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, want, rec.Body.String())
	}
}

func assertHeaderPresent(t *testing.T, rec *httptest.ResponseRecorder, name string) {
	t.Helper()
	if rec.Header().Get(name) == "" {
		t.Fatalf("expected header %s", name)
	}
}

func assertAPIErrorCode(t *testing.T, rec *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error.Code != wantCode {
		t.Fatalf("error code = %q, want %q, body=%s", body.Error.Code, wantCode, rec.Body.String())
	}
}

func TestRespondErrorJSONSuccessShape(t *testing.T) {
	restoreGlobals(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(context.WithValue(req.Context(), requestIDContextKey, "req-42"))
	rec := httptest.NewRecorder()

	respondErrorJSON(rec, req, http.StatusConflict, apiError{
		Code:    "conflict",
		Message: "task conflict",
		Fields:  map[string]string{"title": "duplicate"},
	})

	assertStatus(t, rec, http.StatusConflict)
	var body struct {
		Data  any `json:"data"`
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
		Meta struct {
			RequestID string `json:"request_id"`
		} `json:"meta"`
	}
	decodeJSON(t, rec.Body.Bytes(), &body)
	if body.Error.Code != "conflict" || body.Meta.RequestID != "req-42" {
		t.Fatalf("body = %+v", body)
	}
	if body.Data != nil {
		t.Fatalf("expected no data in error response, got %#v", body.Data)
	}
}

func TestDecodeJSONBodyUnknownFieldAndNilBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString(`{"x":1}`))
	var dst struct {
		Title string `json:"title"`
	}
	if err := decodeJSONBody(req, &dst); err == nil {
		t.Fatal("expected unknown field error")
	}

	req = httptest.NewRequest(http.MethodPost, "/x", nil)
	if err := decodeJSONBody(req, &dst); err == nil {
		t.Fatal("expected empty body error")
	}
}
