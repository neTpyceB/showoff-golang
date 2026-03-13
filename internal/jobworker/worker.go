package jobworker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrQueueFull  = errors.New("job queue is full")
	ErrNotRunning = errors.New("worker is not running")
)

type Processor func(context.Context, Job) error

type Job struct {
	ID       string
	Type     string
	Payload  map[string]string
	Attempts int
}

type DeadJob struct {
	Job       Job
	LastError string
	FailedAt  time.Time
}

type ScheduleSpec struct {
	ID      string
	Every   time.Duration
	JobType string
	Payload map[string]string
}

type Config struct {
	Workers       int
	QueueSize     int
	RetryLimit    int
	SchedulerTick time.Duration
}

type queueItem struct {
	job Job
}

type scheduleState struct {
	spec    ScheduleSpec
	nextRun time.Time
}

type Service struct {
	cfg       Config
	processor Processor

	mu            sync.Mutex
	started       bool
	seq           int64
	queue         chan queueItem
	deadQueue     []DeadJob
	schedules     map[string]scheduleState
	schedulerStop chan struct{}
	workersWG     sync.WaitGroup
	schedulerWG   sync.WaitGroup
}

func New(cfg Config, processor Processor) *Service {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 64
	}
	if cfg.RetryLimit < 0 {
		cfg.RetryLimit = 0
	}
	if cfg.SchedulerTick <= 0 {
		cfg.SchedulerTick = 500 * time.Millisecond
	}
	return &Service{
		cfg:       cfg,
		processor: processor,
		queue:     make(chan queueItem, cfg.QueueSize),
		schedules: map[string]scheduleState{},
	}
}

func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.schedulerStop = make(chan struct{})
	s.mu.Unlock()

	for i := 0; i < s.cfg.Workers; i++ {
		s.workersWG.Add(1)
		go s.workerLoop(ctx)
	}

	s.schedulerWG.Add(1)
	go s.schedulerLoop(ctx)
}

func (s *Service) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	s.started = false
	close(s.schedulerStop)
	close(s.queue)
	s.mu.Unlock()

	s.schedulerWG.Wait()
	s.workersWG.Wait()
}

func (s *Service) Enqueue(jobType string, payload map[string]string) (Job, error) {
	if jobType == "" {
		return Job{}, errors.New("job type is required")
	}

	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return Job{}, ErrNotRunning
	}
	s.seq++
	job := Job{
		ID:      s.newID(),
		Type:    jobType,
		Payload: cloneMap(payload),
	}
	s.mu.Unlock()

	select {
	case s.queue <- queueItem{job: job}:
		return job, nil
	default:
		return Job{}, ErrQueueFull
	}
}

func (s *Service) RegisterSchedule(spec ScheduleSpec) error {
	if spec.ID == "" {
		return errors.New("schedule id is required")
	}
	if spec.Every <= 0 {
		return errors.New("schedule interval must be greater than zero")
	}
	if spec.JobType == "" {
		return errors.New("schedule job type is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedules[spec.ID] = scheduleState{
		spec:    spec,
		nextRun: nowFn().UTC().Add(spec.Every),
	}
	return nil
}

func (s *Service) DeadJobs() []DeadJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeadJob, len(s.deadQueue))
	copy(out, s.deadQueue)
	return out
}

func (s *Service) RunScheduled(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.runScheduledLocked(now.UTC())
}

func (s *Service) workerLoop(ctx context.Context) {
	defer s.workersWG.Done()
	for item := range s.queue {
		s.processWithRetry(ctx, item.job)
	}
}

func (s *Service) processWithRetry(ctx context.Context, job Job) {
	for {
		err := s.processor(ctx, job)
		if err == nil {
			return
		}

		if job.Attempts >= s.cfg.RetryLimit {
			s.mu.Lock()
			s.deadQueue = append(s.deadQueue, DeadJob{
				Job:       job,
				LastError: err.Error(),
				FailedAt:  nowFn().UTC(),
			})
			s.mu.Unlock()
			return
		}

		job.Attempts++
	}
}

func (s *Service) schedulerLoop(ctx context.Context) {
	defer s.schedulerWG.Done()
	ticker := time.NewTicker(s.cfg.SchedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.schedulerStop:
			return
		case now := <-ticker.C:
			s.mu.Lock()
			s.runScheduledLocked(now.UTC())
			s.mu.Unlock()
		}
	}
}

func (s *Service) runScheduledLocked(now time.Time) int {
	if !s.started {
		return 0
	}

	created := 0
	for id, sch := range s.schedules {
		for !sch.nextRun.After(now) {
			s.seq++
			job := Job{
				ID:      s.newID(),
				Type:    sch.spec.JobType,
				Payload: cloneMap(sch.spec.Payload),
			}
			select {
			case s.queue <- queueItem{job: job}:
				created++
			default:
			}
			sch.nextRun = sch.nextRun.Add(sch.spec.Every)
		}
		s.schedules[id] = sch
	}
	return created
}

func (s *Service) newID() string {
	return "job-" + strconvFormatInt(s.seq)
}

var nowFn = time.Now
var strconvFormatInt = func(v int64) string {
	return fmtSprintf("%d", v)
}
var fmtSprintf = func(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
