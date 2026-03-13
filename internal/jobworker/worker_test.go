package jobworker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewConfigDefaults(t *testing.T) {
	s := New(Config{}, func(context.Context, Job) error { return nil })
	if s.cfg.Workers != 1 {
		t.Fatalf("Workers = %d", s.cfg.Workers)
	}
	if s.cfg.QueueSize != 64 {
		t.Fatalf("QueueSize = %d", s.cfg.QueueSize)
	}
	if s.cfg.RetryLimit != 0 {
		t.Fatalf("RetryLimit = %d", s.cfg.RetryLimit)
	}
	if s.cfg.SchedulerTick != 500*time.Millisecond {
		t.Fatalf("SchedulerTick = %s", s.cfg.SchedulerTick)
	}
}

func TestNewConfigNormalization(t *testing.T) {
	s := New(Config{Workers: -1, QueueSize: -3, RetryLimit: -5, SchedulerTick: -1}, func(context.Context, Job) error {
		return nil
	})
	if s.cfg.Workers != 1 || s.cfg.QueueSize != 64 || s.cfg.RetryLimit != 0 || s.cfg.SchedulerTick != 500*time.Millisecond {
		t.Fatalf("normalized cfg = %+v", s.cfg)
	}
}

func TestEnqueueRequiresRunningAndType(t *testing.T) {
	s := New(Config{}, func(context.Context, Job) error { return nil })

	if _, err := s.Enqueue("", nil); err == nil {
		t.Fatal("expected type error")
	}
	if _, err := s.Enqueue("email", nil); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v", err)
	}
}

func TestWorkerProcessesJobAndClonePayload(t *testing.T) {
	var (
		mu       sync.Mutex
		seenJobs []Job
		done     = make(chan struct{}, 1)
	)
	s := New(Config{Workers: 1, QueueSize: 4}, func(_ context.Context, job Job) error {
		mu.Lock()
		seenJobs = append(seenJobs, job)
		mu.Unlock()
		done <- struct{}{}
		return nil
	})

	s.Start(context.Background())
	defer s.Stop()

	payload := map[string]string{"k": "v1"}
	job, err := s.Enqueue("email", payload)
	if err != nil {
		t.Fatalf("enqueue err = %v", err)
	}
	payload["k"] = "mutated"

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting job")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenJobs) != 1 {
		t.Fatalf("seen jobs = %d", len(seenJobs))
	}
	if seenJobs[0].ID != job.ID {
		t.Fatalf("id = %q want %q", seenJobs[0].ID, job.ID)
	}
	if seenJobs[0].Payload["k"] != "v1" {
		t.Fatalf("payload clone failed: %+v", seenJobs[0].Payload)
	}
}

func TestQueueFull(t *testing.T) {
	block := make(chan struct{})
	s := New(Config{Workers: 1, QueueSize: 1}, func(_ context.Context, _ Job) error {
		<-block
		return nil
	})

	s.Start(context.Background())
	defer func() {
		close(block)
		s.Stop()
	}()

	if _, err := s.Enqueue("a", nil); err != nil {
		t.Fatalf("enqueue1 err = %v", err)
	}

	var gotQueueFull bool
	for _, jt := range []string{"b", "c", "d"} {
		_, err := s.Enqueue(jt, nil)
		if errors.Is(err, ErrQueueFull) {
			gotQueueFull = true
			break
		}
		if err != nil {
			t.Fatalf("enqueue %s err = %v", jt, err)
		}
	}
	if !gotQueueFull {
		t.Fatal("expected queue full error")
	}
}

func TestRetryLimitAndDeadQueue(t *testing.T) {
	restoreTimeHelpers(t)

	nowFn = func() time.Time { return time.Date(2026, 3, 13, 12, 0, 0, 0, time.UTC) }
	attempts := 0
	done := make(chan struct{}, 1)
	s := New(Config{Workers: 1, QueueSize: 2, RetryLimit: 2}, func(_ context.Context, _ Job) error {
		attempts++
		if attempts == 3 {
			done <- struct{}{}
		}
		return errors.New("always fail")
	})

	s.Start(context.Background())
	defer s.Stop()

	if _, err := s.Enqueue("retry", map[string]string{"x": "1"}); err != nil {
		t.Fatalf("enqueue err = %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting retries")
	}

	dead := s.DeadJobs()
	if len(dead) != 1 {
		t.Fatalf("dead len = %d", len(dead))
	}
	if dead[0].Job.Attempts != 2 {
		t.Fatalf("attempts = %d", dead[0].Job.Attempts)
	}
	if dead[0].LastError != "always fail" {
		t.Fatalf("last error = %q", dead[0].LastError)
	}
	if dead[0].FailedAt.Format(time.RFC3339) != "2026-03-13T12:00:00Z" {
		t.Fatalf("failed at = %s", dead[0].FailedAt)
	}
}

func TestRegisterScheduleValidationAndRun(t *testing.T) {
	restoreTimeHelpers(t)
	base := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	nowFn = func() time.Time { return base }

	var processed []Job
	mu := sync.Mutex{}
	s := New(Config{Workers: 1, QueueSize: 10}, func(_ context.Context, j Job) error {
		mu.Lock()
		processed = append(processed, j)
		mu.Unlock()
		return nil
	})
	s.Start(context.Background())
	defer s.Stop()

	if err := s.RegisterSchedule(ScheduleSpec{}); err == nil {
		t.Fatal("expected empty schedule id error")
	}
	if err := s.RegisterSchedule(ScheduleSpec{ID: "x", Every: 0, JobType: "t"}); err == nil {
		t.Fatal("expected invalid interval error")
	}
	if err := s.RegisterSchedule(ScheduleSpec{ID: "x", Every: time.Second}); err == nil {
		t.Fatal("expected missing job type error")
	}

	if err := s.RegisterSchedule(ScheduleSpec{
		ID:      "heartbeat",
		Every:   time.Second,
		JobType: "heartbeat",
		Payload: map[string]string{"a": "1"},
	}); err != nil {
		t.Fatalf("register err = %v", err)
	}

	if created := s.RunScheduled(base.Add(2500 * time.Millisecond)); created != 2 {
		t.Fatalf("created = %d", created)
	}

	waitUntil(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(processed) == 2
	})

	mu.Lock()
	defer mu.Unlock()
	if processed[0].Type != "heartbeat" || processed[1].Type != "heartbeat" {
		t.Fatalf("processed types = %+v", processed)
	}
}

func TestRunScheduledWhenStoppedAndQueueDrop(t *testing.T) {
	restoreTimeHelpers(t)
	nowFn = func() time.Time { return time.Date(2026, 3, 13, 9, 0, 0, 0, time.UTC) }

	s := New(Config{Workers: 1, QueueSize: 1}, func(context.Context, Job) error { return nil })
	if created := s.RunScheduled(nowFn().UTC().Add(time.Second)); created != 0 {
		t.Fatalf("created when stopped = %d", created)
	}

	s.Start(context.Background())
	defer s.Stop()

	if err := s.RegisterSchedule(ScheduleSpec{
		ID:      "drop",
		Every:   time.Second,
		JobType: "drop",
	}); err != nil {
		t.Fatalf("register err = %v", err)
	}

	// fill queue
	if _, err := s.Enqueue("filler", nil); err != nil {
		t.Fatalf("enqueue filler err = %v", err)
	}
	created := s.RunScheduled(nowFn().UTC().Add(2 * time.Second))
	if created > 1 {
		t.Fatalf("created should be bounded by queue space, got %d", created)
	}
}

func TestStartStopIdempotentAndSchedulerStopsOnContext(t *testing.T) {
	s := New(Config{Workers: 1, QueueSize: 2, SchedulerTick: 10 * time.Millisecond}, func(context.Context, Job) error { return nil })
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	s.Start(ctx)
	cancel()
	s.Stop()
	s.Stop()
}

func TestSchedulerLoopTicksAndEnqueues(t *testing.T) {
	restoreTimeHelpers(t)
	nowFn = time.Now

	done := make(chan struct{}, 1)
	s := New(Config{Workers: 1, QueueSize: 8, SchedulerTick: 5 * time.Millisecond}, func(context.Context, Job) error {
		done <- struct{}{}
		return nil
	})
	s.Start(context.Background())
	defer s.Stop()

	if err := s.RegisterSchedule(ScheduleSpec{
		ID:      "ticker",
		Every:   5 * time.Millisecond,
		JobType: "tick",
	}); err != nil {
		t.Fatalf("register err = %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting scheduled job")
	}
}

func TestSchedulerStopsOnContextCancel(t *testing.T) {
	s := New(Config{Workers: 1, QueueSize: 2, SchedulerTick: 5 * time.Millisecond}, func(context.Context, Job) error {
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	cancel()
	time.Sleep(20 * time.Millisecond)
	s.Stop()
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func restoreTimeHelpers(t *testing.T) {
	t.Helper()
	oldNow := nowFn
	t.Cleanup(func() {
		nowFn = oldNow
	})
}
