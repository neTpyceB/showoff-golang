# In-Process Background Job Worker

Concurrency baseline for background processing in the app process.

## Location

- Worker implementation: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/jobworker/worker.go`
- Tests: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/jobworker/worker_test.go`

## Features

- Worker pool with configurable parallelism
- Bounded in-memory queue with backpressure (`ErrQueueFull`)
- Retry loop with configurable retry limit
- Dead queue for permanently failed jobs
- Periodic scheduler that enqueues jobs by interval
- Start/stop lifecycle for clean shutdown

## Core Types

- `Config`: workers, queue size, retry limit, scheduler tick
- `Job`: id, type, payload, attempts
- `DeadJob`: failed job + last error + failed timestamp
- `ScheduleSpec`: id, interval, job type, payload
- `Processor`: function executed by workers

## Usage

```go
svc := jobworker.New(jobworker.Config{
    Workers: 4,
    QueueSize: 256,
    RetryLimit: 3,
    SchedulerTick: 500 * time.Millisecond,
}, func(ctx context.Context, job jobworker.Job) error {
    // process job
    return nil
})

svc.Start(context.Background())
defer svc.Stop()

_, _ = svc.Enqueue("send-email", map[string]string{"to": "user@example.com"})

_ = svc.RegisterSchedule(jobworker.ScheduleSpec{
    ID: "heartbeat",
    Every: 30 * time.Second,
    JobType: "heartbeat",
})
```

## Commands

```bash
make test
make cover
```
