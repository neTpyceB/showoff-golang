package redisworker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestNewValidation(t *testing.T) {
	_, err := New(Config{}, func(context.Context, Message) error { return nil })
	if err == nil {
		t.Fatal("expected addr validation error")
	}

	_, err = New(Config{Addr: "127.0.0.1:6379", Mode: "invalid", QueueKey: "q"}, func(context.Context, Message) error { return nil })
	if err == nil {
		t.Fatal("expected mode validation error")
	}
	_, err = New(Config{Addr: "127.0.0.1:6379", Mode: ModeList}, func(context.Context, Message) error { return nil })
	if err == nil {
		t.Fatal("expected queue key validation error")
	}

	_, err = New(Config{Addr: "127.0.0.1:6379", Mode: ModeList, QueueKey: "q"}, nil)
	if err == nil {
		t.Fatal("expected processor validation error")
	}

	_, err = New(Config{Addr: "127.0.0.1:6379", Mode: ModeStream, QueueKey: "q"}, func(context.Context, Message) error { return nil })
	if err == nil {
		t.Fatal("expected stream group validation error")
	}

	s, err := New(Config{Addr: "127.0.0.1:6379", Mode: ModeStream, QueueKey: "q", Group: "g"}, func(context.Context, Message) error { return nil })
	if err != nil {
		t.Fatalf("new err = %v", err)
	}
	if s.cfg.Workers != 1 || s.cfg.BlockTimeout != time.Second || s.cfg.ConsumerPrefix != "consumer" {
		t.Fatalf("defaults not applied: %+v", s.cfg)
	}

	s, err = New(Config{Addr: "127.0.0.1:6379", Mode: ModeStream, QueueKey: "q", Group: "g", ConsumerPrefix: "x"}, func(context.Context, Message) error { return nil })
	if err != nil {
		t.Fatalf("new err = %v", err)
	}
	if s.cfg.ConsumerPrefix != "x" {
		t.Fatalf("consumer prefix override lost: %+v", s.cfg)
	}
}

func TestListModeEndToEndAndSafeShutdown(t *testing.T) {
	mr := miniredis.RunT(t)

	var (
		mu    sync.Mutex
		seen  []Message
		doneC = make(chan struct{}, 1)
	)
	service, err := New(Config{
		Addr:         mr.Addr(),
		Mode:         ModeList,
		QueueKey:     "jobs:list",
		Workers:      2,
		BlockTimeout: 50 * time.Millisecond,
	}, func(_ context.Context, m Message) error {
		mu.Lock()
		seen = append(seen, m)
		mu.Unlock()
		doneC <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("new err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("start err = %v", err)
	}

	if err := service.EnqueueList(context.Background(), "job-1"); err != nil {
		t.Fatalf("enqueue err = %v", err)
	}

	select {
	case <-doneC:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting list job")
	}

	if err := service.Stop(); err != nil {
		t.Fatalf("stop err = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("seen len = %d", len(seen))
	}
	if seen[0].Raw != "job-1" || seen[0].Queue != "jobs:list" {
		t.Fatalf("unexpected message: %+v", seen[0])
	}
}

func TestStreamModeEndToEndAckAndProcessorErrorNoAck(t *testing.T) {
	mr := miniredis.RunT(t)

	processed := make(chan Message, 3)
	failNext := true
	service, err := New(Config{
		Addr:           mr.Addr(),
		Mode:           ModeStream,
		QueueKey:       "jobs:stream",
		Group:          "workers",
		ConsumerPrefix: "c",
		Workers:        1,
		BlockTimeout:   50 * time.Millisecond,
	}, func(_ context.Context, m Message) error {
		processed <- m
		if failNext {
			failNext = false
			return errors.New("fail once")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("new err = %v", err)
	}

	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("start err = %v", err)
	}

	if err := service.EnqueueStream(context.Background(), map[string]string{"k": "v"}); err != nil {
		t.Fatalf("enqueue stream err = %v", err)
	}
	if err := service.EnqueueStream(context.Background(), map[string]string{"k": "ok"}); err != nil {
		t.Fatalf("enqueue stream err = %v", err)
	}

	select {
	case <-processed:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting stream message")
	}
	select {
	case msg := <-processed:
		if msg.Fields["k"] != "ok" && msg.Fields["k"] != "v" {
			t.Fatalf("unexpected fields: %+v", msg.Fields)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting second stream message")
	}

	if err := service.Stop(); err != nil {
		t.Fatalf("stop err = %v", err)
	}
}

func TestEnqueueModeMismatch(t *testing.T) {
	mr := miniredis.RunT(t)

	listService, err := New(Config{Addr: mr.Addr(), Mode: ModeList, QueueKey: "q"}, func(context.Context, Message) error { return nil })
	if err != nil {
		t.Fatalf("new err = %v", err)
	}
	if err := listService.EnqueueStream(context.Background(), map[string]string{"x": "1"}); err == nil {
		t.Fatal("expected mode mismatch error")
	}

	streamService, err := New(Config{Addr: mr.Addr(), Mode: ModeStream, QueueKey: "s", Group: "g"}, func(context.Context, Message) error { return nil })
	if err != nil {
		t.Fatalf("new err = %v", err)
	}
	if err := streamService.EnqueueList(context.Background(), "x"); err == nil {
		t.Fatal("expected mode mismatch error")
	}
}

func TestStartBusyGroupAllowed(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRedisClient{
		xGroupCreateFn: func(context.Context, string, string, string) *redis.StatusCmd {
			cmd := redis.NewStatusCmd(ctx)
			cmd.SetErr(errors.New("BUSYGROUP Consumer Group name already exists"))
			return cmd
		},
		xReadGroupFn: func(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
			cmd := redis.NewXStreamSliceCmd(ctx)
			cmd.SetErr(context.Canceled)
			return cmd
		},
		closeFn: func() error { return nil },
	}
	s := newWithClient(Config{Addr: "x", Mode: ModeStream, QueueKey: "s", Group: "g", BlockTimeout: 10 * time.Millisecond}, func(context.Context, Message) error {
		return nil
	}, fake)

	if err := s.Start(ctx); err != nil {
		t.Fatalf("start err = %v", err)
	}
	if err := s.Stop(); err != nil {
		t.Fatalf("stop err = %v", err)
	}
}

func TestStartGroupCreateError(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRedisClient{
		xGroupCreateFn: func(context.Context, string, string, string) *redis.StatusCmd {
			cmd := redis.NewStatusCmd(ctx)
			cmd.SetErr(errors.New("boom"))
			return cmd
		},
		closeFn: func() error { return nil },
	}
	s := newWithClient(Config{Addr: "x", Mode: ModeStream, QueueKey: "s", Group: "g"}, func(context.Context, Message) error {
		return nil
	}, fake)

	if err := s.Start(ctx); err == nil {
		t.Fatal("expected start error")
	}
}

func TestConsumeListAndStreamBranches(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRedisClient{
		blPopFn: func(context.Context, time.Duration, ...string) *redis.StringSliceCmd {
			cmd := redis.NewStringSliceCmd(ctx)
			cmd.SetVal([]string{"only-key"})
			return cmd
		},
		xReadGroupFn: func(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
			cmd := redis.NewXStreamSliceCmd(ctx)
			cmd.SetVal([]redis.XStream{{
				Stream: "stream",
				Messages: []redis.XMessage{{
					ID:     "1-0",
					Values: map[string]any{"a": 7},
				}},
			}})
			return cmd
		},
		xAckFn: func(context.Context, string, string, ...string) *redis.IntCmd {
			cmd := redis.NewIntCmd(ctx)
			cmd.SetErr(errors.New("ack failed"))
			return cmd
		},
		closeFn: func() error { return nil },
	}

	listSvc := newWithClient(Config{Mode: ModeList, QueueKey: "k", BlockTimeout: time.Millisecond, Workers: 1}, func(context.Context, Message) error {
		return nil
	}, fake)
	if err := listSvc.consumeList(ctx); err != nil {
		t.Fatalf("consumeList err = %v", err)
	}

	streamSvc := newWithClient(Config{Mode: ModeStream, QueueKey: "stream", Group: "g", ConsumerPrefix: "c", BlockTimeout: time.Millisecond, Workers: 1}, func(context.Context, Message) error {
		return nil
	}, fake)
	if err := streamSvc.consumeStream(ctx, 0); err != nil {
		t.Fatalf("consumeStream err = %v", err)
	}

	fake.blPopFn = func(context.Context, time.Duration, ...string) *redis.StringSliceCmd {
		cmd := redis.NewStringSliceCmd(ctx)
		cmd.SetErr(redis.Nil)
		return cmd
	}
	if err := listSvc.consumeList(ctx); err != nil {
		t.Fatalf("consumeList nil err = %v", err)
	}

	fake.xReadGroupFn = func(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
		cmd := redis.NewXStreamSliceCmd(ctx)
		cmd.SetErr(redis.Nil)
		return cmd
	}
	if err := streamSvc.consumeStream(ctx, 0); err != nil {
		t.Fatalf("consumeStream nil err = %v", err)
	}
}

func TestConsumeErrorsAndStopCloseError(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRedisClient{
		blPopFn: func(context.Context, time.Duration, ...string) *redis.StringSliceCmd {
			cmd := redis.NewStringSliceCmd(ctx)
			cmd.SetErr(errors.New("blpop failed"))
			return cmd
		},
		xReadGroupFn: func(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
			cmd := redis.NewXStreamSliceCmd(ctx)
			cmd.SetErr(errors.New("xread failed"))
			return cmd
		},
		closeFn: func() error { return errors.New("close failed") },
	}
	s := newWithClient(Config{Mode: ModeList, QueueKey: "k", Workers: 1, BlockTimeout: time.Millisecond}, func(context.Context, Message) error {
		return nil
	}, fake)

	if err := s.consumeList(ctx); err == nil {
		t.Fatal("expected consumeList error")
	}

	s.cfg.Mode = ModeStream
	s.cfg.Group = "g"
	s.cfg.ConsumerPrefix = "c"
	if err := s.consumeStream(ctx, 0); err == nil {
		t.Fatal("expected consumeStream error")
	}

	if err := s.Start(ctx); err != nil {
		t.Fatalf("start err = %v", err)
	}
	if err := s.Stop(); err == nil {
		t.Fatal("expected close error")
	}
}

func TestHelpers(t *testing.T) {
	if !isBusyGroup(errors.New("BUSYGROUP x")) {
		t.Fatal("expected busy group true")
	}
	if isBusyGroup(errors.New("other")) {
		t.Fatal("expected busy group false")
	}
}

func TestWorkerLoopRetriesOnNonCancelError(t *testing.T) {
	ctx := context.Background()
	var calls int
	fake := &fakeRedisClient{
		blPopFn: func(context.Context, time.Duration, ...string) *redis.StringSliceCmd {
			cmd := redis.NewStringSliceCmd(ctx)
			calls++
			if calls == 1 {
				cmd.SetErr(errors.New("temporary"))
				return cmd
			}
			cmd.SetErr(context.Canceled)
			return cmd
		},
	}
	s := newWithClient(Config{Mode: ModeList, QueueKey: "k", BlockTimeout: time.Millisecond, Workers: 1}, func(context.Context, Message) error {
		return nil
	}, fake)

	s.wg.Add(1)
	s.workerLoop(context.Background(), 0)
	if calls < 2 {
		t.Fatalf("calls = %d, expected at least 2", calls)
	}
}

type fakeRedisClient struct {
	blPopFn        func(context.Context, time.Duration, ...string) *redis.StringSliceCmd
	lPushFn        func(context.Context, string, ...any) *redis.IntCmd
	xAddFn         func(context.Context, *redis.XAddArgs) *redis.StringCmd
	xGroupCreateFn func(context.Context, string, string, string) *redis.StatusCmd
	xReadGroupFn   func(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	xAckFn         func(context.Context, string, string, ...string) *redis.IntCmd
	closeFn        func() error
}

func (f *fakeRedisClient) BLPop(ctx context.Context, d time.Duration, keys ...string) *redis.StringSliceCmd {
	if f.blPopFn != nil {
		return f.blPopFn(ctx, d, keys...)
	}
	cmd := redis.NewStringSliceCmd(ctx)
	cmd.SetErr(context.Canceled)
	return cmd
}

func (f *fakeRedisClient) LPush(ctx context.Context, key string, values ...any) *redis.IntCmd {
	if f.lPushFn != nil {
		return f.lPushFn(ctx, key, values...)
	}
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(1)
	return cmd
}

func (f *fakeRedisClient) XAdd(ctx context.Context, args *redis.XAddArgs) *redis.StringCmd {
	if f.xAddFn != nil {
		return f.xAddFn(ctx, args)
	}
	cmd := redis.NewStringCmd(ctx)
	cmd.SetVal("1-0")
	return cmd
}

func (f *fakeRedisClient) XGroupCreateMkStream(ctx context.Context, stream string, group string, start string) *redis.StatusCmd {
	if f.xGroupCreateFn != nil {
		return f.xGroupCreateFn(ctx, stream, group, start)
	}
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeRedisClient) XReadGroup(ctx context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	if f.xReadGroupFn != nil {
		return f.xReadGroupFn(ctx, args)
	}
	cmd := redis.NewXStreamSliceCmd(ctx)
	cmd.SetErr(context.Canceled)
	return cmd
}

func (f *fakeRedisClient) XAck(ctx context.Context, stream string, group string, ids ...string) *redis.IntCmd {
	if f.xAckFn != nil {
		return f.xAckFn(ctx, stream, group, ids...)
	}
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(1)
	return cmd
}

func (f *fakeRedisClient) Close() error {
	if f.closeFn != nil {
		return f.closeFn()
	}
	return nil
}
