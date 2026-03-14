package eventpipe

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPublisherRunOnce(t *testing.T) {
	oldNow := nowFn
	nowFn = func() time.Time { return time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFn = oldNow })

	repo := &fakeOutboxRepo{
		events: []OutboxEvent{
			{ID: 1, Payload: []byte(`{"ok":1}`), AggregateType: "order", AggregateID: 10, TraceID: "t1", CorrelationID: "c1", Attempts: 0},
			{ID: 2, Payload: []byte(`{"ok":2}`), AggregateType: "order", AggregateID: 11, TraceID: "t2", CorrelationID: "c2", Attempts: 7},
			{ID: 3, Payload: []byte(`{"ok":3}`), AggregateType: "order", AggregateID: 12, TraceID: "t3", CorrelationID: "c3", Attempts: 1},
		},
	}
	broker := &fakePublisherBroker{
		failByEventID: map[string]error{
			"order:11": errors.New("publish fail dead"),
			"order:12": errors.New("publish fail retry"),
		},
	}
	p := NewPublisher(repo, broker, PublisherConfig{
		Topic:        "orders.events",
		BatchSize:    10,
		MaxAttempts:  8,
		BaseBackoff:  time.Second,
		ServiceName:  "p",
		LoggerPrintf: func(string, ...any) {},
	})
	n, err := p.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(repo.published) != 1 || repo.published[0] != 1 {
		t.Fatalf("published=%v", repo.published)
	}
	if len(repo.dead) != 1 || repo.dead[0] != 2 {
		t.Fatalf("dead=%v", repo.dead)
	}
	if len(repo.retry) != 1 || repo.retry[0] != 3 {
		t.Fatalf("retry=%v", repo.retry)
	}
}

func TestPublisherRunAndErrors(t *testing.T) {
	repo := &fakeOutboxRepo{fetchErr: errors.New("fetch fail")}
	p := NewPublisher(repo, &fakePublisherBroker{}, PublisherConfig{
		DisableTicker: true,
		LoggerPrintf:  func(string, ...any) {},
	})
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("expected run error")
	}

	repo = &fakeOutboxRepo{events: []OutboxEvent{{ID: 1, AggregateType: "a", AggregateID: 1}}}
	p = NewPublisher(repo, &fakePublisherBroker{}, PublisherConfig{
		DisableTicker: true,
		LoggerPrintf:  func(string, ...any) {},
	})
	repo.markPublishedErr = errors.New("mark published fail")
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("expected mark published error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p = NewPublisher(&fakeOutboxRepo{}, &fakePublisherBroker{}, PublisherConfig{
		PollInterval:  1 * time.Millisecond,
		LoggerPrintf:  func(string, ...any) {},
		DisableTicker: false,
	})
	if err := p.Run(ctx); err == nil {
		t.Fatal("expected canceled error")
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	go func() {
		time.Sleep(3 * time.Millisecond)
		cancel2()
	}()
	p = NewPublisher(&fakeOutboxRepo{}, &fakePublisherBroker{}, PublisherConfig{
		PollInterval:  1 * time.Millisecond,
		LoggerPrintf:  func(string, ...any) {},
		DisableTicker: false,
	})
	if err := p.Run(ctx2); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}

	repo = &fakeOutboxRepo{
		events:      []OutboxEvent{{ID: 1, AggregateType: "a", AggregateID: 1, Attempts: 7}},
		markDeadErr: errors.New("dead err"),
	}
	p = NewPublisher(repo, &fakePublisherBroker{failByEventID: map[string]error{"a:1": errors.New("pub err")}}, PublisherConfig{
		DisableTicker: true,
		LoggerPrintf:  func(string, ...any) {},
	})
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("expected mark dead error")
	}

	repo = &fakeOutboxRepo{
		events:       []OutboxEvent{{ID: 1, AggregateType: "a", AggregateID: 1, Attempts: 0}},
		markRetryErr: errors.New("retry err"),
	}
	p = NewPublisher(repo, &fakePublisherBroker{failByEventID: map[string]error{"a:1": errors.New("pub err")}}, PublisherConfig{
		DisableTicker: true,
		LoggerPrintf:  func(string, ...any) {},
	})
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("expected mark retry error")
	}

	repo = &fakeOutboxRepo{
		failFetchAfter: 2,
		fetchErr:       errors.New("stop after tick"),
	}
	p = NewPublisher(repo, &fakePublisherBroker{}, PublisherConfig{
		PollInterval:  1 * time.Millisecond,
		LoggerPrintf:  func(string, ...any) {},
		DisableTicker: false,
	})
	if err := p.Run(context.Background()); err == nil {
		t.Fatal("expected stop error")
	}
}

func TestConsumerRunOnce(t *testing.T) {
	oldNow := nowFn
	nowFn = func() time.Time { return time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { nowFn = oldNow })

	b := &fakeConsumerBroker{
		messages: []BrokerConsumedMessage{
			{ID: "1-0", Payload: []byte(`{"order_id":1}`), TraceID: "t1", CorrelationID: "c1"},
			{ID: "2-0", Payload: []byte(`{"order_id":2}`), TraceID: "t2", CorrelationID: "c2"},
			{ID: "2-0", Payload: []byte(`{"order_id":2}`), TraceID: "t2", CorrelationID: "c2"},
		},
	}
	h := &fakeHandler{failByID: map[int64]int{2: 2}}
	dlq := &fakeDLQ{}
	c := NewConsumer(b, dlq, h, ConsumerConfig{
		MaxAttempts:  2,
		LoggerPrintf: func(string, ...any) {},
	})

	n, err := c.RunOnce(context.Background())
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if len(b.acked) != 2 { // one success + one dlq after retries
		t.Fatalf("acked=%v", b.acked)
	}
	if len(dlq.items) != 1 {
		t.Fatalf("dlq=%v", dlq.items)
	}
}

func TestConsumerErrorPathsAndProjectionHandler(t *testing.T) {
	c := NewConsumer(&fakeConsumerBroker{readErr: errors.New("read fail")}, &fakeDLQ{}, &fakeHandler{}, ConsumerConfig{
		LoggerPrintf: func(string, ...any) {},
	})
	if _, err := c.RunOnce(context.Background()); err == nil {
		t.Fatal("expected read error")
	}

	c = NewConsumer(&fakeConsumerBroker{messages: []BrokerConsumedMessage{{ID: "1", Payload: []byte(`{}`)}}}, &fakeDLQ{}, &fakeHandler{}, ConsumerConfig{
		LoggerPrintf: func(string, ...any) {},
	})
	c.broker = &fakeConsumerBroker{messages: []BrokerConsumedMessage{{ID: "1", Payload: []byte(`{}`)}, {ID: "2", Payload: []byte(`{}`)}}, ackErr: errors.New("ack fail")}
	if _, err := c.RunOnce(context.Background()); err == nil {
		t.Fatal("expected ack error")
	}

	ph := NewProjectionHandler(&fakeProjectionRepo{})
	if err := ph.Handle(context.Background(), []byte(`{"order_id":1,"payment_status":"paid","total_cents":1}`)); err != nil {
		t.Fatalf("projection handle err=%v", err)
	}
	if err := ph.Handle(context.Background(), []byte(`{bad`)); err == nil {
		t.Fatal("expected json error")
	}
	if err := ph.Handle(context.Background(), []byte(`{"order_id":0}`)); err == nil {
		t.Fatal("expected invalid order id error")
	}

	c = NewConsumer(&fakeConsumerBroker{messages: []BrokerConsumedMessage{{ID: "m1", Payload: []byte(`{"order_id":2}`)}}}, &fakeDLQErr{}, &fakeHandler{failByID: map[int64]int{2: 1}}, ConsumerConfig{
		MaxAttempts:  1,
		LoggerPrintf: func(string, ...any) {},
	})
	if _, err := c.RunOnce(context.Background()); err == nil {
		t.Fatal("expected dlq insert error")
	}

	c = NewConsumer(&fakeConsumerBroker{messages: []BrokerConsumedMessage{{ID: "m2", Payload: []byte(`{"order_id":2}`)}}, ackErr: errors.New("ack fail")}, &fakeDLQ{}, &fakeHandler{failByID: map[int64]int{2: 1}}, ConsumerConfig{
		MaxAttempts:  1,
		LoggerPrintf: func(string, ...any) {},
	})
	if _, err := c.RunOnce(context.Background()); err == nil {
		t.Fatal("expected ack error in dlq path")
	}

	cfgP := NewPublisher(&fakeOutboxRepo{}, &fakePublisherBroker{}, PublisherConfig{})
	if cfgP.cfg.Topic == "" || cfgP.cfg.BatchSize == 0 || cfgP.cfg.MaxAttempts == 0 {
		t.Fatal("publisher defaults not applied")
	}
	cfgC := NewConsumer(&fakeConsumerBroker{}, &fakeDLQ{}, &fakeHandler{}, ConsumerConfig{})
	if cfgC.cfg.Stream == "" || cfgC.cfg.Group == "" || cfgC.cfg.ConsumerName == "" {
		t.Fatal("consumer defaults not applied")
	}

	if min(1, 2) != 1 || min(3, 2) != 2 || min(2, 2) != 2 {
		t.Fatal("min mismatch")
	}
}

type fakeOutboxRepo struct {
	events           []OutboxEvent
	fetchErr         error
	fetchCalls       int
	failFetchAfter   int
	markPublishedErr error
	markRetryErr     error
	markDeadErr      error
	published, retry []int64
	dead             []int64
}

func (f *fakeOutboxRepo) FetchPending(context.Context, int, time.Time) ([]OutboxEvent, error) {
	f.fetchCalls++
	if f.failFetchAfter > 0 && f.fetchCalls >= f.failFetchAfter {
		return nil, f.fetchErr
	}
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.events, nil
}
func (f *fakeOutboxRepo) MarkPublished(_ context.Context, id int64, _ time.Time) error {
	if f.markPublishedErr != nil {
		return f.markPublishedErr
	}
	f.published = append(f.published, id)
	return nil
}
func (f *fakeOutboxRepo) MarkRetry(_ context.Context, id int64, _ int, _ time.Time, _ string) error {
	if f.markRetryErr != nil {
		return f.markRetryErr
	}
	f.retry = append(f.retry, id)
	return nil
}
func (f *fakeOutboxRepo) MarkDead(_ context.Context, id int64, _ int, _ string) error {
	if f.markDeadErr != nil {
		return f.markDeadErr
	}
	f.dead = append(f.dead, id)
	return nil
}

type fakePublisherBroker struct {
	failByEventID map[string]error
}

func (b *fakePublisherBroker) Publish(_ context.Context, _ string, m BrokerMessage) error {
	if b.failByEventID != nil {
		if err, ok := b.failByEventID[m.Key]; ok {
			return err
		}
	}
	return nil
}

type fakeConsumerBroker struct {
	messages []BrokerConsumedMessage
	readErr  error
	ackErr   error
	acked    []string
}

func (b *fakeConsumerBroker) Read(context.Context, string, string, string, int, time.Duration) ([]BrokerConsumedMessage, error) {
	if b.readErr != nil {
		return nil, b.readErr
	}
	out := b.messages
	b.messages = nil
	return out, nil
}
func (b *fakeConsumerBroker) Ack(context.Context, string, string, ...string) error {
	if b.ackErr != nil {
		return b.ackErr
	}
	b.acked = append(b.acked, "ack")
	return nil
}

type fakeHandler struct {
	failByID map[int64]int
}

func (h *fakeHandler) Handle(_ context.Context, payload []byte) error {
	if len(payload) == 0 || payload[0] != '{' {
		return errors.New("bad payload")
	}
	if h.failByID != nil && string(payload) == `{"order_id":2}` {
		if h.failByID[2] > 0 {
			h.failByID[2]--
			return errors.New("processing failed")
		}
	}
	return nil
}

type fakeDLQ struct{ items []string }

func (d *fakeDLQ) InsertDLQ(context.Context, string, string, []byte, string, int, string, string, time.Time) error {
	d.items = append(d.items, "1")
	return nil
}

type fakeDLQErr struct{}

func (fakeDLQErr) InsertDLQ(context.Context, string, string, []byte, string, int, string, string, time.Time) error {
	return errors.New("dlq fail")
}

type fakeProjectionRepo struct{}

func (fakeProjectionRepo) UpsertOrderProjection(context.Context, OrderCreatedEvent, time.Time) error {
	return nil
}
