package metricscollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrQueueFull = errors.New("metrics queue is full")

type Event struct {
	Source     string `json:"source"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

type Counter struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

type LatencyMetric struct {
	Name  string  `json:"name"`
	Count int64   `json:"count"`
	SumMS int64   `json:"sum_ms"`
	AvgMS float64 `json:"avg_ms"`
}

type Snapshot struct {
	Counters []Counter       `json:"counters"`
	Latency  []LatencyMetric `json:"latency"`
}

type Config struct {
	Workers       int
	QueueSize     int
	FlushInterval time.Duration
}

type Store interface {
	Apply(context.Context, map[string]int64, map[string]latencyAgg) error
	Snapshot(context.Context) (Snapshot, error)
}

type latencyAgg struct {
	SumMS int64
	Count int64
}

type Service struct {
	cfg   Config
	store Store

	queue chan Event

	mu              sync.Mutex
	started         bool
	pendingCounters map[string]int64
	pendingLatency  map[string]latencyAgg

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(cfg Config, store Store) *Service {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 256
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = time.Second
	}
	if store == nil {
		store = NewMemoryStore()
	}

	return &Service{
		cfg:             cfg,
		store:           store,
		queue:           make(chan Event, cfg.QueueSize),
		pendingCounters: map[string]int64{},
		pendingLatency:  map[string]latencyAgg{},
	}
}

func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.mu.Unlock()

	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.workerLoop(runCtx)
	}

	s.wg.Add(1)
	go s.flushLoop(runCtx)
}

func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	return s.Flush(context.Background())
}

func (s *Service) Ingest(event Event) error {
	if event.Source == "" || event.Name == "" || event.Status == "" {
		return errors.New("source, name, and status are required")
	}
	if event.DurationMS < 0 {
		return errors.New("duration_ms must be >= 0")
	}

	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return errors.New("collector is not running")
	}

	select {
	case s.queue <- event:
		return nil
	default:
		return ErrQueueFull
	}
}

func (s *Service) Flush(ctx context.Context) error {
	counters, lat := s.drainPending()
	if len(counters) == 0 && len(lat) == 0 {
		return nil
	}
	return s.store.Apply(ctx, counters, lat)
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	return s.store.Snapshot(ctx)
}

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		_ = s.Ingest(Event{
			Source:     "http",
			Name:       r.Method + ":" + r.URL.Path,
			Status:     fmt.Sprintf("%d", rec.StatusCode()),
			DurationMS: time.Since(start).Milliseconds(),
		})
	})
}

func (s *Service) workerLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-s.queue:
			s.processEvent(event)
		}
	}
}

func (s *Service) flushLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Flush(context.Background())
		}
	}
}

func (s *Service) processEvent(event Event) {
	counterKey := fmt.Sprintf("%s.%s.%s", event.Source, event.Name, event.Status)
	latKey := fmt.Sprintf("%s.%s", event.Source, event.Name)

	s.mu.Lock()
	s.pendingCounters[counterKey]++
	agg := s.pendingLatency[latKey]
	agg.Count++
	agg.SumMS += event.DurationMS
	s.pendingLatency[latKey] = agg
	s.mu.Unlock()
}

func (s *Service) drainPending() (map[string]int64, map[string]latencyAgg) {
	s.mu.Lock()
	defer s.mu.Unlock()

	counters := make(map[string]int64, len(s.pendingCounters))
	for k, v := range s.pendingCounters {
		counters[k] = v
	}
	lat := make(map[string]latencyAgg, len(s.pendingLatency))
	for k, v := range s.pendingLatency {
		lat[k] = v
	}
	s.pendingCounters = map[string]int64{}
	s.pendingLatency = map[string]latencyAgg{}
	return counters, lat
}

func NewHandler(s *Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /metrics/events", func(w http.ResponseWriter, r *http.Request) {
		var in Event
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_json"})
			return
		}
		if err := s.Ingest(in); err != nil {
			if errors.Is(err, ErrQueueFull) {
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "queue_full"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	})

	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		snap, err := s.Snapshot(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "snapshot_failed"})
			return
		}
		writeJSON(w, http.StatusOK, snap)
	})

	return mux
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
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

type memoryStore struct {
	mu       sync.Mutex
	counters map[string]int64
	lat      map[string]latencyAgg
}

func NewMemoryStore() Store {
	return &memoryStore{
		counters: map[string]int64{},
		lat:      map[string]latencyAgg{},
	}
}

func (m *memoryStore) Apply(_ context.Context, counters map[string]int64, lat map[string]latencyAgg) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range counters {
		m.counters[k] += v
	}
	for k, v := range lat {
		agg := m.lat[k]
		agg.Count += v.Count
		agg.SumMS += v.SumMS
		m.lat[k] = agg
	}
	return nil
}

func (m *memoryStore) Snapshot(context.Context) (Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return buildSnapshot(m.counters, m.lat), nil
}

type RedisStore struct {
	client *redis.Client
}

var redisHGetAllFn = func(ctx context.Context, client *redis.Client, key string) (map[string]string, error) {
	return client.HGetAll(ctx, key).Result()
}

func NewRedisStore(addr, password string, db int) *RedisStore {
	return &RedisStore{client: redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})}
}

func (r *RedisStore) Apply(ctx context.Context, counters map[string]int64, lat map[string]latencyAgg) error {
	pipe := r.client.Pipeline()
	for k, v := range counters {
		pipe.HIncrBy(ctx, "metrics:counters", k, v)
	}
	for k, v := range lat {
		pipe.HIncrBy(ctx, "metrics:latency:sum", k, v.SumMS)
		pipe.HIncrBy(ctx, "metrics:latency:count", k, v.Count)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *RedisStore) Snapshot(ctx context.Context) (Snapshot, error) {
	countersRaw, err := redisHGetAllFn(ctx, r.client, "metrics:counters")
	if err != nil {
		return Snapshot{}, err
	}
	sumRaw, err := redisHGetAllFn(ctx, r.client, "metrics:latency:sum")
	if err != nil {
		return Snapshot{}, err
	}
	countRaw, err := redisHGetAllFn(ctx, r.client, "metrics:latency:count")
	if err != nil {
		return Snapshot{}, err
	}

	counters := map[string]int64{}
	for k, v := range countersRaw {
		parsed, parseErr := parseInt64(v)
		if parseErr != nil {
			continue
		}
		counters[k] = parsed
	}
	lat := map[string]latencyAgg{}
	for k, v := range sumRaw {
		sum, parseErr := parseInt64(v)
		if parseErr != nil {
			continue
		}
		agg := lat[k]
		agg.SumMS = sum
		lat[k] = agg
	}
	for k, v := range countRaw {
		count, parseErr := parseInt64(v)
		if parseErr != nil {
			continue
		}
		agg := lat[k]
		agg.Count = count
		lat[k] = agg
	}
	return buildSnapshot(counters, lat), nil
}

func (r *RedisStore) Close() error {
	return r.client.Close()
}

func parseInt64(raw string) (int64, error) {
	var n int64
	_, err := fmt.Sscan(raw, &n)
	return n, err
}

func buildSnapshot(counters map[string]int64, lat map[string]latencyAgg) Snapshot {
	counterKeys := make([]string, 0, len(counters))
	for k := range counters {
		counterKeys = append(counterKeys, k)
	}
	sort.Strings(counterKeys)

	latKeys := make([]string, 0, len(lat))
	for k := range lat {
		latKeys = append(latKeys, k)
	}
	sort.Strings(latKeys)

	snap := Snapshot{Counters: make([]Counter, 0, len(counterKeys)), Latency: make([]LatencyMetric, 0, len(latKeys))}
	for _, k := range counterKeys {
		snap.Counters = append(snap.Counters, Counter{Name: k, Value: counters[k]})
	}
	for _, k := range latKeys {
		agg := lat[k]
		avg := 0.0
		if agg.Count > 0 {
			avg = float64(agg.SumMS) / float64(agg.Count)
		}
		snap.Latency = append(snap.Latency, LatencyMetric{Name: k, Count: agg.Count, SumMS: agg.SumMS, AvgMS: avg})
	}
	return snap
}
