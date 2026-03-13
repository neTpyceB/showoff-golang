package redisworker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type Mode string

const (
	ModeList   Mode = "list"
	ModeStream Mode = "stream"
)

type Message struct {
	Queue    string
	Raw      string
	StreamID string
	Fields   map[string]string
}

type Processor func(context.Context, Message) error

type Config struct {
	Addr           string
	Password       string
	DB             int
	Mode           Mode
	QueueKey       string
	Workers        int
	BlockTimeout   time.Duration
	Group          string
	ConsumerPrefix string
}

type Service struct {
	cfg       Config
	processor Processor
	client    redisClient

	cancel context.CancelFunc
	wg     sync.WaitGroup

	seq uint64
}

type redisClient interface {
	BLPop(context.Context, time.Duration, ...string) *redis.StringSliceCmd
	LPush(context.Context, string, ...any) *redis.IntCmd
	XAdd(context.Context, *redis.XAddArgs) *redis.StringCmd
	XGroupCreateMkStream(context.Context, string, string, string) *redis.StatusCmd
	XReadGroup(context.Context, *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	XAck(context.Context, string, string, ...string) *redis.IntCmd
	Close() error
}

func New(cfg Config, processor Processor) (*Service, error) {
	if processor == nil {
		return nil, errors.New("processor is required")
	}
	if cfg.Addr == "" {
		return nil, errors.New("redis addr is required")
	}
	if cfg.QueueKey == "" {
		return nil, errors.New("queue key is required")
	}
	if cfg.Mode != ModeList && cfg.Mode != ModeStream {
		return nil, errors.New("mode must be list or stream")
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.BlockTimeout <= 0 {
		cfg.BlockTimeout = time.Second
	}
	if cfg.Mode == ModeStream {
		if cfg.Group == "" {
			return nil, errors.New("stream group is required")
		}
		if cfg.ConsumerPrefix == "" {
			cfg.ConsumerPrefix = "consumer"
		}
	}

	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	return newWithClient(cfg, processor, client), nil
}

func newWithClient(cfg Config, processor Processor, client redisClient) *Service {
	return &Service{
		cfg:       cfg,
		processor: processor,
		client:    client,
	}
}

func (s *Service) Start(ctx context.Context) error {
	if s.cfg.Mode == ModeStream {
		err := s.client.XGroupCreateMkStream(ctx, s.cfg.QueueKey, s.cfg.Group, "$").Err()
		if err != nil && !isBusyGroup(err) {
			return fmt.Errorf("create stream group: %w", err)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.workerLoop(runCtx, i)
	}

	return nil
}

func (s *Service) Stop() error {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	return s.client.Close()
}

func (s *Service) EnqueueList(ctx context.Context, payload string) error {
	if s.cfg.Mode != ModeList {
		return errors.New("service mode is not list")
	}
	return s.client.LPush(ctx, s.cfg.QueueKey, payload).Err()
}

func (s *Service) EnqueueStream(ctx context.Context, fields map[string]string) error {
	if s.cfg.Mode != ModeStream {
		return errors.New("service mode is not stream")
	}
	values := make(map[string]any, len(fields))
	for k, v := range fields {
		values[k] = v
	}
	return s.client.XAdd(ctx, &redis.XAddArgs{Stream: s.cfg.QueueKey, Values: values}).Err()
}

func (s *Service) workerLoop(ctx context.Context, workerIdx int) {
	defer s.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		var err error
		if s.cfg.Mode == ModeList {
			err = s.consumeList(ctx)
		} else {
			err = s.consumeStream(ctx, workerIdx)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
}

func (s *Service) consumeList(ctx context.Context) error {
	res, err := s.client.BLPop(ctx, s.cfg.BlockTimeout, s.cfg.QueueKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	}
	if len(res) != 2 {
		return nil
	}
	return s.processor(ctx, Message{Queue: s.cfg.QueueKey, Raw: res[1]})
}

func (s *Service) consumeStream(ctx context.Context, workerIdx int) error {
	consumer := s.cfg.ConsumerPrefix + "-" + strconv.Itoa(workerIdx) + "-" + strconv.FormatUint(atomic.AddUint64(&s.seq, 1), 10)
	streams, err := s.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    s.cfg.Group,
		Consumer: consumer,
		Streams:  []string{s.cfg.QueueKey, ">"},
		Count:    1,
		Block:    s.cfg.BlockTimeout,
		NoAck:    false,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	}

	for _, strm := range streams {
		for _, msg := range strm.Messages {
			fields := map[string]string{}
			for k, v := range msg.Values {
				fields[k] = fmt.Sprint(v)
			}
			procErr := s.processor(ctx, Message{
				Queue:    strm.Stream,
				StreamID: msg.ID,
				Fields:   fields,
			})
			if procErr == nil {
				_ = s.client.XAck(ctx, s.cfg.QueueKey, s.cfg.Group, msg.ID).Err()
			}
		}
	}
	return nil
}

func isBusyGroup(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "BUSYGROUP") || strings.Contains(err.Error(), "Consumer Group name already exists"))
}
