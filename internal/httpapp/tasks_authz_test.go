package httpapp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTaskHandlersAuthAndRBACBranches(t *testing.T) {
	restoreGlobals(t)
	nowFn = func() time.Time { return time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC) }

	repo := newMemoryTaskRepository()
	api := newTaskAPIWithRepository(repo)

	seed := func(owner int64, title string) int64 {
		created, err := repo.Create(context.Background(), taskInput{OwnerUserID: owner, Title: title}, nowFn().UTC())
		if err != nil {
			t.Fatalf("seed create err: %v", err)
		}
		return created.ID
	}

	ownerID := seed(100, "owner-task")
	_ = seed(200, "other-task")

	t.Run("list unauthorized", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
		api.listTasks(rec, req)
		assertStatus(t, rec, http.StatusUnauthorized)
	})

	t.Run("list user filtered", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
		req = req.WithContext(withAuthPrincipal(req.Context(), 100, "user"))
		api.listTasks(rec, req)
		assertStatus(t, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "owner-task") || strings.Contains(rec.Body.String(), "other-task") {
			t.Fatalf("body=%s", rec.Body.String())
		}
	})

	t.Run("list admin sees all", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
		req = req.WithContext(withAuthPrincipal(req.Context(), 999, "admin"))
		api.listTasks(rec, req)
		assertStatus(t, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), "owner-task") || !strings.Contains(rec.Body.String(), "other-task") {
			t.Fatalf("body=%s", rec.Body.String())
		}
	})

	t.Run("create unauthorized", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"title":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		api.createTask(rec, req)
		assertStatus(t, rec, http.StatusUnauthorized)
	})

	t.Run("create sets owner", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"title":"owned"}`))
		req = req.WithContext(withAuthPrincipal(req.Context(), 300, "user"))
		req.Header.Set("Content-Type", "application/json")
		api.createTask(rec, req)
		assertStatus(t, rec, http.StatusCreated)
		if !strings.Contains(rec.Body.String(), "owned") {
			t.Fatalf("body=%s", rec.Body.String())
		}
	})

	t.Run("get forbidden", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks/"+itoa(ownerID), nil)
		req.SetPathValue("id", itoa(ownerID))
		req = req.WithContext(withAuthPrincipal(req.Context(), 999, "user"))
		api.getTask(rec, req)
		assertStatus(t, rec, http.StatusForbidden)
	})

	t.Run("get unauthorized", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks/"+itoa(ownerID), nil)
		req.SetPathValue("id", itoa(ownerID))
		api.getTask(rec, req)
		assertStatus(t, rec, http.StatusUnauthorized)
	})

	t.Run("get not found", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks/999999", nil)
		req.SetPathValue("id", "999999")
		req = req.WithContext(withAuthPrincipal(req.Context(), 100, "user"))
		api.getTask(rec, req)
		assertStatus(t, rec, http.StatusNotFound)
	})

	t.Run("get with user id only context (role fallback)", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/tasks/"+itoa(ownerID), nil)
		req.SetPathValue("id", itoa(ownerID))
		req = req.WithContext(withAuthUserID(req.Context(), 100))
		api.getTask(rec, req)
		assertStatus(t, rec, http.StatusOK)
	})

	t.Run("update unauthorized", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/tasks/"+itoa(ownerID), strings.NewReader(`{"title":"x"}`))
		req.SetPathValue("id", itoa(ownerID))
		req.Header.Set("Content-Type", "application/json")
		api.updateTask(rec, req)
		assertStatus(t, rec, http.StatusUnauthorized)
	})

	t.Run("update forbidden", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/tasks/"+itoa(ownerID), strings.NewReader(`{"title":"x","note":"","done":false}`))
		req.SetPathValue("id", itoa(ownerID))
		req = req.WithContext(withAuthPrincipal(req.Context(), 999, "user"))
		req.Header.Set("Content-Type", "application/json")
		api.updateTask(rec, req)
		assertStatus(t, rec, http.StatusForbidden)
	})

	t.Run("update admin allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/tasks/"+itoa(ownerID), strings.NewReader(`{"title":"admin-updated","note":"","done":false}`))
		req.SetPathValue("id", itoa(ownerID))
		req = req.WithContext(withAuthPrincipal(req.Context(), 999, "admin"))
		req.Header.Set("Content-Type", "application/json")
		api.updateTask(rec, req)
		assertStatus(t, rec, http.StatusOK)
	})

	t.Run("delete unauthorized", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/tasks/"+itoa(ownerID), nil)
		req.SetPathValue("id", itoa(ownerID))
		api.deleteTask(rec, req)
		assertStatus(t, rec, http.StatusUnauthorized)
	})

	t.Run("delete forbidden", func(t *testing.T) {
		id := seed(500, "forbidden-delete")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/tasks/"+itoa(id), nil)
		req.SetPathValue("id", itoa(id))
		req = req.WithContext(withAuthPrincipal(req.Context(), 111, "user"))
		api.deleteTask(rec, req)
		assertStatus(t, rec, http.StatusForbidden)
	})

	t.Run("delete admin allowed", func(t *testing.T) {
		id := seed(600, "admin-delete")
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/tasks/"+itoa(id), nil)
		req.SetPathValue("id", itoa(id))
		req = req.WithContext(withAuthPrincipal(req.Context(), 1, "admin"))
		api.deleteTask(rec, req)
		assertStatus(t, rec, http.StatusNoContent)
	})
}

func TestAuthPrincipalHelpersBranches(t *testing.T) {
	ctx := withAuthUserID(context.Background(), 10)
	uid, role, ok := authPrincipalFromContext(ctx)
	if !ok || uid != 10 || role != "user" {
		t.Fatalf("uid=%d role=%s ok=%v", uid, role, ok)
	}

	ctx = withAuthPrincipal(context.Background(), 20, "admin")
	uid, role, ok = authPrincipalFromContext(ctx)
	if !ok || uid != 20 || role != "admin" {
		t.Fatalf("uid=%d role=%s ok=%v", uid, role, ok)
	}

	a := newAuthAPI()
	if r := a.userRoleByID(999); r != "user" {
		t.Fatalf("role=%s", r)
	}
	a.mu.Lock()
	a.usersByID[1] = authUser{ID: 1, Email: "x@x.com", Role: ""}
	a.usersByID[2] = authUser{ID: 2, Email: "a@a.com", Role: "admin"}
	a.mu.Unlock()
	if r := a.userRoleByID(1); r != "user" {
		t.Fatalf("role=%s", r)
	}
	if r := a.userRoleByID(2); r != "admin" {
		t.Fatalf("role=%s", r)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

type taskRepoAfterGetError struct {
	updateErr error
	deleteErr error
}

func (f taskRepoAfterGetError) List(context.Context) ([]task, error) { return []task{}, nil }
func (f taskRepoAfterGetError) Create(context.Context, taskInput, time.Time) (task, error) {
	return task{ID: 1, OwnerUserID: 1, Title: "x"}, nil
}
func (f taskRepoAfterGetError) Get(context.Context, int64) (task, error) {
	return task{ID: 1, OwnerUserID: 1, Title: "x"}, nil
}
func (f taskRepoAfterGetError) Update(context.Context, int64, taskInput, time.Time) (task, error) {
	return task{}, f.updateErr
}
func (f taskRepoAfterGetError) Delete(context.Context, int64) error { return f.deleteErr }

func TestTaskHandlersRepositoryErrorsAfterAuthorization(t *testing.T) {
	restoreGlobals(t)
	api := newTaskAPIWithRepository(taskRepoAfterGetError{updateErr: errTaskNotFound, deleteErr: errors.New("delete failed")})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/1", nil)
	req.SetPathValue("id", "1")
	req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
	api.getTask(rec, req)
	assertStatus(t, rec, http.StatusOK)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/tasks/1", strings.NewReader(`{"title":"x","note":"","done":false}`))
	req.SetPathValue("id", "1")
	req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
	req.Header.Set("Content-Type", "application/json")
	api.updateTask(rec, req)
	assertStatus(t, rec, http.StatusNotFound)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/tasks/1", nil)
	req.SetPathValue("id", "1")
	req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
	api.deleteTask(rec, req)
	assertStatus(t, rec, http.StatusInternalServerError)
}

type taskRepoGetErr struct{}

func (taskRepoGetErr) List(context.Context) ([]task, error)                       { return nil, nil }
func (taskRepoGetErr) Create(context.Context, taskInput, time.Time) (task, error) { return task{}, nil }
func (taskRepoGetErr) Get(context.Context, int64) (task, error) {
	return task{}, errors.New("get failed")
}
func (taskRepoGetErr) Update(context.Context, int64, taskInput, time.Time) (task, error) {
	return task{}, nil
}
func (taskRepoGetErr) Delete(context.Context, int64) error { return nil }

func TestGetTaskInvalidIDAndRepositoryError(t *testing.T) {
	restoreGlobals(t)
	api := newTaskAPIWithRepository(taskRepoGetErr{})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/nope", nil)
	req.SetPathValue("id", "nope")
	req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
	api.getTask(rec, req)
	assertStatus(t, rec, http.StatusBadRequest)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/tasks/1", nil)
	req.SetPathValue("id", "1")
	req = req.WithContext(withAuthPrincipal(req.Context(), 1, "user"))
	api.getTask(rec, req)
	assertStatus(t, rec, http.StatusInternalServerError)
}
