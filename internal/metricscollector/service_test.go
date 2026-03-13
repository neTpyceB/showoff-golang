package metricscollector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

type stubStore struct {
	applyErr    error
	snapshotErr error
	counters    map[string]int64
	lat         map[string]latencyAgg
	applyCalls  int
}

func (s *stubStore) Apply(_ context.Context, counters map[string]int64, lat map[string]latencyAgg) error {
	if s.applyErr != nil {
		return s.applyErr
	}
	s.applyCalls++
	if s.counters == nil {
		s.counters = map[string]int64{}
	}
	if s.lat == nil {
		s.lat = map[string]latencyAgg{}
	}
	for k, v := range counters {
		s.counters[k] += v
	}
	for k, v := range lat {
		agg := s.lat[k]
		agg.SumMS += v.SumMS
		agg.Count += v.Count
		s.lat[k] = agg
	}
	return nil
}

func (s *stubStore) Snapshot(context.Context) (Snapshot, error) {
	if s.snapshotErr != nil {
		return Snapshot{}, s.snapshotErr
	}
	return buildSnapshot(s.counters, s.lat), nil
}

func TestNewDefaultsAndStartStop(t *testing.T) {
	s := New(Config{}, nil)
	if s.cfg.Workers != 2 || s.cfg.QueueSize != 256 || s.cfg.FlushInterval != time.Second {
		t.Fatalf("cfg = %+v", s.cfg)
	}

	s.Start(context.Background())
	s.Start(context.Background())
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("stop err = %v", err)
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("stop err = %v", err)
	}
}

func TestStopTimeout(t *testing.T) {
	s := New(Config{}, NewMemoryStore())
	s.Start(context.Background())

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if err := s.Stop(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestIngestValidationAndQueueFull(t *testing.T) {
	s := New(Config{Workers: 1, QueueSize: 1, FlushInterval: time.Hour}, NewMemoryStore())
	if err := s.Ingest(Event{}); err == nil {
		t.Fatal("expected validation error")
	}
	if err := s.Ingest(Event{Source: "a", Name: "b", Status: "ok", DurationMS: 1}); err == nil {
		t.Fatal("expected not running error")
	}
	s.Start(context.Background())
	defer s.Stop(context.Background())
	if err := s.Ingest(Event{Source: "a", Name: "b", Status: "ok", DurationMS: -1}); err == nil {
		t.Fatal("expected duration validation error")
	}

	_ = s.Ingest(Event{Source: "a", Name: "b", Status: "ok", DurationMS: 1})
	gotFull := false
	for i := 0; i < 500; i++ {
		err := s.Ingest(Event{Source: "a", Name: "b", Status: "ok", DurationMS: 1})
		if errors.Is(err, ErrQueueFull) {
			gotFull = true
			break
		}
	}
	if !gotFull {
		t.Fatal("expected queue full")
	}
}

func TestFlushAndSnapshot(t *testing.T) {
	store := &stubStore{}
	s := New(Config{Workers: 1, QueueSize: 8, FlushInterval: time.Hour}, store)
	s.Start(context.Background())
	defer s.Stop(context.Background())

	if err := s.Ingest(Event{Source: "api", Name: "hello", Status: "200", DurationMS: 10}); err != nil {
		t.Fatalf("ingest err = %v", err)
	}
	if err := s.Ingest(Event{Source: "api", Name: "hello", Status: "200", DurationMS: 30}); err != nil {
		t.Fatalf("ingest err = %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool {
		_ = s.Flush(context.Background())
		snap, _ := s.Snapshot(context.Background())
		return len(snap.Counters) == 1 && len(snap.Latency) == 1
	})

	snap, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot err = %v", err)
	}
	if snap.Counters[0].Value != 2 {
		t.Fatalf("counter = %+v", snap.Counters[0])
	}
	if snap.Latency[0].SumMS != 40 || snap.Latency[0].Count != 2 {
		t.Fatalf("latency = %+v", snap.Latency[0])
	}
}

func TestFlushApplyError(t *testing.T) {
	store := &stubStore{applyErr: errors.New("apply failed")}
	s := New(Config{Workers: 1, QueueSize: 4, FlushInterval: time.Hour}, store)
	s.Start(context.Background())
	defer s.Stop(context.Background())
	_ = s.Ingest(Event{Source: "api", Name: "x", Status: "200", DurationMS: 1})
	waitUntil(t, time.Second, func() bool { return len(s.queue) == 0 })
	if err := s.Flush(context.Background()); err == nil {
		t.Fatal("expected flush error")
	}
}

func TestFlushLoopTickerPath(t *testing.T) {
	store := &stubStore{}
	s := New(Config{Workers: 1, QueueSize: 8, FlushInterval: 10 * time.Millisecond}, store)
	s.Start(context.Background())
	defer s.Stop(context.Background())

	if err := s.Ingest(Event{Source: "api", Name: "tick", Status: "200", DurationMS: 5}); err != nil {
		t.Fatalf("ingest err = %v", err)
	}
	waitUntil(t, time.Second, func() bool { return store.applyCalls > 0 })
}

func TestHandlerEndpoints(t *testing.T) {
	s := New(Config{Workers: 1, QueueSize: 16, FlushInterval: time.Hour}, NewMemoryStore())
	h := NewHandler(s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/metrics/events", bytes.NewBufferString("{"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}

	s.Start(context.Background())
	defer s.Stop(context.Background())

	rec = httptest.NewRecorder()
	body := bytes.NewBufferString(`{"source":"api","name":"hello","status":"200","duration_ms":22}`)
	req = httptest.NewRequest(http.MethodPost, "/metrics/events", body)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}

	waitUntil(t, time.Second, func() bool {
		_ = s.Flush(context.Background())
		snap, _ := s.Snapshot(context.Background())
		return len(snap.Counters) == 1
	})

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var snap Snapshot
	if err := json.NewDecoder(rec.Body).Decode(&snap); err != nil {
		t.Fatalf("decode err = %v", err)
	}
	if len(snap.Counters) == 0 {
		t.Fatal("expected counters")
	}
}

func TestHandlerSnapshotErrorAndQueueFull(t *testing.T) {
	store := &stubStore{snapshotErr: errors.New("snap failed")}
	s := New(Config{Workers: 1, QueueSize: 1, FlushInterval: time.Hour}, store)
	h := NewHandler(s)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}

	s.Start(context.Background())
	defer s.Stop(context.Background())
	for i := 0; i < 500; i++ {
		_ = s.Ingest(Event{Source: "api", Name: "x", Status: "200", DurationMS: 1})
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/metrics/events", bytes.NewBufferString(`{"source":"api","name":"x","status":"200","duration_ms":1}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests && rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/metrics/events", bytes.NewBufferString(`{"source":"api","status":"200","duration_ms":1}`))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHandlerQueueFullBranch(t *testing.T) {
	s := New(Config{Workers: 1, QueueSize: 1, FlushInterval: time.Hour}, NewMemoryStore())
	// Force "running" state without workers so queue can fill deterministically.
	s.mu.Lock()
	s.started = true
	s.mu.Unlock()

	h := NewHandler(s)
	makeReq := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/metrics/events", bytes.NewBufferString(`{"source":"api","name":"x","status":"200","duration_ms":1}`))
		h.ServeHTTP(rec, req)
		return rec
	}

	first := makeReq()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", first.Code)
	}
	second := makeReq()
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d", second.Code)
	}
}

func TestMiddlewareIngest(t *testing.T) {
	s := New(Config{Workers: 1, QueueSize: 16, FlushInterval: time.Hour}, NewMemoryStore())
	s.Start(context.Background())
	defer s.Stop(context.Background())

	h := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}

	waitUntil(t, time.Second, func() bool {
		_ = s.Flush(context.Background())
		snap, _ := s.Snapshot(context.Background())
		return len(snap.Counters) > 0
	})
}

func TestStatusRecorderDefaults(t *testing.T) {
	r := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if got := r.StatusCode(); got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}
}

func TestRedisStoreApplyAndSnapshot(t *testing.T) {
	mr := miniredis.RunT(t)
	store := NewRedisStore(mr.Addr(), "", 0)
	defer store.Close()

	if err := store.Apply(context.Background(), map[string]int64{"a": 2}, map[string]latencyAgg{"api.x": {SumMS: 30, Count: 2}}); err != nil {
		t.Fatalf("apply err = %v", err)
	}
	snap, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot err = %v", err)
	}
	if len(snap.Counters) != 1 || snap.Counters[0].Value != 2 {
		t.Fatalf("counters = %+v", snap.Counters)
	}
	if len(snap.Latency) != 1 || snap.Latency[0].AvgMS != 15 {
		t.Fatalf("latency = %+v", snap.Latency)
	}

	mr.HSet("metrics:counters", "bad", "x")
	mr.HSet("metrics:latency:sum", "bad-sum", "x")
	mr.HSet("metrics:latency:count", "bad-count", "y")
	_, err = store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot with bad values should skip invalid, err=%v", err)
	}
}

func TestRedisStoreSnapshotErrorsByStage(t *testing.T) {
	mr := miniredis.RunT(t)
	store := NewRedisStore(mr.Addr(), "", 0)
	defer store.Close()

	oldHGetAll := redisHGetAllFn
	defer func() { redisHGetAllFn = oldHGetAll }()

	call := 0
	redisHGetAllFn = func(context.Context, *redis.Client, string) (map[string]string, error) {
		call++
		if call == 1 {
			return nil, errors.New("counters failed")
		}
		return map[string]string{}, nil
	}
	if _, err := store.Snapshot(context.Background()); err == nil {
		t.Fatal("expected first stage error")
	}

	call = 0
	redisHGetAllFn = func(context.Context, *redis.Client, string) (map[string]string, error) {
		call++
		if call == 2 {
			return nil, errors.New("sum failed")
		}
		return map[string]string{}, nil
	}
	if _, err := store.Snapshot(context.Background()); err == nil {
		t.Fatal("expected second stage error")
	}

	call = 0
	redisHGetAllFn = func(context.Context, *redis.Client, string) (map[string]string, error) {
		call++
		if call == 3 {
			return nil, errors.New("count failed")
		}
		return map[string]string{}, nil
	}
	if _, err := store.Snapshot(context.Background()); err == nil {
		t.Fatal("expected third stage error")
	}
}

func TestBuildSnapshotAndParseInt64(t *testing.T) {
	snap := buildSnapshot(map[string]int64{"b": 1, "a": 2}, map[string]latencyAgg{"x": {SumMS: 0, Count: 0}})
	if snap.Counters[0].Name != "a" || snap.Counters[1].Name != "b" {
		t.Fatalf("sorted counters = %+v", snap.Counters)
	}
	if snap.Latency[0].AvgMS != 0 {
		t.Fatalf("avg = %f", snap.Latency[0].AvgMS)
	}

	if _, err := parseInt64("bad"); err == nil {
		t.Fatal("expected parse error")
	}
	if n, err := parseInt64("42"); err != nil || n != 42 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
