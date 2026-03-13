# Redis-Backed Queue Worker

Redis-backed background worker supporting two queue strategies.

## Location

- Implementation: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/redisworker/worker.go`
- Tests: `/Users/vadimsduboiss/Codebase/showoff-golang/internal/redisworker/worker_test.go`

## Features

- `ModeList`: Redis List queue using blocking `BLPOP`
- `ModeStream`: Redis Stream queue using `XREADGROUP`
- Worker pool (`Workers`)
- Blocking consume loop with configurable timeout (`BlockTimeout`)
- Safe shutdown via context cancellation + worker wait
- Stream consumer group bootstrap (`XGROUP CREATE ... MKSTREAM`)
- Auto ACK on successful stream processing (`XACK`)

## API

- `New(Config, Processor)`
- `Start(ctx)`
- `Stop()`
- `EnqueueList(ctx, payload)`
- `EnqueueStream(ctx, fields)`

`Processor` receives normalized `Message`:

- `Queue`
- `Raw` (list mode payload)
- `StreamID` (stream message ID)
- `Fields` (stream message fields)

## Minimal Usage

```go
svc, err := redisworker.New(redisworker.Config{
    Addr:         "redis:6379",
    Mode:         redisworker.ModeList,
    QueueKey:     "jobs:list",
    Workers:      4,
    BlockTimeout: time.Second,
}, func(ctx context.Context, msg redisworker.Message) error {
    // handle msg
    return nil
})
if err != nil {
    return err
}

if err := svc.Start(context.Background()); err != nil {
    return err
}
defer svc.Stop()

_ = svc.EnqueueList(context.Background(), "job-payload")
```

## Commands

```bash
make test
make cover
```
