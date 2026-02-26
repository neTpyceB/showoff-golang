package httpapp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type taskAPI struct {
	repo taskRepository
}

type taskRepository interface {
	List(context.Context) ([]task, error)
	Create(context.Context, taskInput, time.Time) (task, error)
	Get(context.Context, int64) (task, error)
	Update(context.Context, int64, taskInput, time.Time) (task, error)
	Delete(context.Context, int64) error
}

type memoryTaskRepository struct {
	mu     sync.Mutex
	nextID int64
	tasks  map[int64]task
}

type task struct {
	ID        int64
	Title     string
	Note      string
	Done      bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type taskInput struct {
	Title string `json:"title"`
	Note  string `json:"note"`
	Done  bool   `json:"done"`
}

type taskResponse struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Note      string `json:"note"`
	Done      bool   `json:"done"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

var errTaskNotFound = errors.New("task not found")

func newTaskAPI() *taskAPI {
	return newTaskAPIWithRepository(newMemoryTaskRepository())
}

func newTaskAPIWithRepository(repo taskRepository) *taskAPI {
	return &taskAPI{
		repo: repo,
	}
}

func newMemoryTaskRepository() *memoryTaskRepository {
	return &memoryTaskRepository{
		nextID: 1,
		tasks:  make(map[int64]task),
	}
}

func (a *taskAPI) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := a.repo.List(r.Context())
	if err != nil {
		respondInternalServerError(w, r)
		return
	}
	items := make([]taskResponse, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, toTaskResponse(t))
	}
	respondJSON(w, r, http.StatusOK, map[string]any{"tasks": items})
}

func (a *taskAPI) createTask(w http.ResponseWriter, r *http.Request) {
	var in taskInput
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "invalid_json",
			Message: "invalid JSON request body",
		})
		return
	}

	in.Title = strings.TrimSpace(in.Title)
	if fields := validateTaskInput(in); len(fields) > 0 {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "validation_error",
			Message: "request validation failed",
			Fields:  fields,
		})
		return
	}

	created, err := a.repo.Create(r.Context(), in, nowFn().UTC())
	if err != nil {
		respondInternalServerError(w, r)
		return
	}
	respondJSON(w, r, http.StatusCreated, map[string]any{"task": toTaskResponse(created)})
}

func (a *taskAPI) getTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTaskID(w, r)
	if !ok {
		return
	}
	t, err := a.repo.Get(r.Context(), id)
	if err != nil {
		respondTaskRepositoryError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, map[string]any{"task": toTaskResponse(t)})
}

func (a *taskAPI) updateTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTaskID(w, r)
	if !ok {
		return
	}

	var in taskInput
	if err := decodeJSONBody(r, &in); err != nil {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "invalid_json",
			Message: "invalid JSON request body",
		})
		return
	}

	in.Title = strings.TrimSpace(in.Title)
	if fields := validateTaskInput(in); len(fields) > 0 {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "validation_error",
			Message: "request validation failed",
			Fields:  fields,
		})
		return
	}

	updated, err := a.repo.Update(r.Context(), id, in, nowFn().UTC())
	if err != nil {
		respondTaskRepositoryError(w, r, err)
		return
	}
	respondJSON(w, r, http.StatusOK, map[string]any{"task": toTaskResponse(updated)})
}

func (a *taskAPI) deleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := parseTaskID(w, r)
	if !ok {
		return
	}
	if err := a.repo.Delete(r.Context(), id); err != nil {
		respondTaskRepositoryError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseTaskID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	rawID := r.PathValue("id")
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		respondErrorJSON(w, r, http.StatusBadRequest, apiError{
			Code:    "invalid_id",
			Message: "task id must be a positive integer",
		})
		return 0, false
	}
	return id, true
}

func respondTaskNotFound(w http.ResponseWriter, r *http.Request) {
	respondErrorJSON(w, r, http.StatusNotFound, apiError{
		Code:    "not_found",
		Message: "task not found",
	})
}

func respondInternalServerError(w http.ResponseWriter, r *http.Request) {
	respondErrorJSON(w, r, http.StatusInternalServerError, apiError{
		Code:    "internal_error",
		Message: "internal server error",
	})
}

func respondTaskRepositoryError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errTaskNotFound) {
		respondTaskNotFound(w, r)
		return
	}
	loggerPrintfFn("task repository error path=%s err=%v", r.URL.Path, err)
	respondInternalServerError(w, r)
}

func validateTaskInput(in taskInput) map[string]string {
	fields := map[string]string{}
	if in.Title == "" {
		fields["title"] = "title is required"
	}
	if len(in.Title) > 200 {
		fields["title"] = "title must be at most 200 characters"
	}
	if len(in.Note) > 2000 {
		fields["note"] = "note must be at most 2000 characters"
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func (s *memoryTaskRepository) List(context.Context) ([]task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]task, 0, len(s.tasks))
	for _, t := range s.tasks {
		items = append(items, t)
	}
	slices.SortFunc(items, compareTaskByID)
	return items, nil
}

func (s *memoryTaskRepository) Create(_ context.Context, in taskInput, ts time.Time) (task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t := task{
		ID:        s.nextID,
		Title:     in.Title,
		Note:      in.Note,
		Done:      in.Done,
		CreatedAt: ts,
		UpdatedAt: ts,
	}
	s.tasks[t.ID] = t
	s.nextID++
	return t, nil
}

func (s *memoryTaskRepository) Get(_ context.Context, id int64) (task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tasks[id]
	if !ok {
		return task{}, errTaskNotFound
	}
	return t, nil
}

func (s *memoryTaskRepository) Update(_ context.Context, id int64, in taskInput, ts time.Time) (task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tasks[id]
	if !ok {
		return task{}, errTaskNotFound
	}
	t.Title = in.Title
	t.Note = in.Note
	t.Done = in.Done
	t.UpdatedAt = ts
	s.tasks[id] = t
	return t, nil
}

func (s *memoryTaskRepository) Delete(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tasks[id]; !ok {
		return errTaskNotFound
	}
	delete(s.tasks, id)
	return nil
}

func toTaskResponse(t task) taskResponse {
	return taskResponse{
		ID:        t.ID,
		Title:     t.Title,
		Note:      t.Note,
		Done:      t.Done,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func compareTaskByID(a, b task) int {
	if a.ID < b.ID {
		return -1
	}
	if a.ID > b.ID {
		return 1
	}
	return 0
}
