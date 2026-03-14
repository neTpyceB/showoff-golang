package eventpipe

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestPostgresRepo(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewPostgresRepo(db)
	ts := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)

	rows := sqlmock.NewRows([]string{"id", "event_type", "aggregate_type", "aggregate_id", "payload", "trace_id", "correlation_id", "attempts"}).
		AddRow(int64(1), "order.created", "order", int64(10), `{"a":1}`, "t", "c", 0)
	mock.ExpectQuery("SELECT id, event_type").WithArgs(ts, 10).WillReturnRows(rows)
	got, err := repo.FetchPending(context.Background(), 10, ts)
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%v err=%v", got, err)
	}

	mock.ExpectExec("UPDATE outbox_events").WithArgs(int64(1), ts).WillReturnResult(sqlmock.NewResult(1, 1))
	if err := repo.MarkPublished(context.Background(), 1, ts); err != nil {
		t.Fatalf("err=%v", err)
	}

	mock.ExpectExec("UPDATE outbox_events").WithArgs(int64(1), 2, ts, "x").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := repo.MarkRetry(context.Background(), 1, 2, ts, "x"); err != nil {
		t.Fatalf("err=%v", err)
	}

	mock.ExpectExec("UPDATE outbox_events").WithArgs(int64(1), 3, "dead").WillReturnResult(sqlmock.NewResult(1, 1))
	if err := repo.MarkDead(context.Background(), 1, 3, "dead"); err != nil {
		t.Fatalf("err=%v", err)
	}

	mock.ExpectExec("INSERT INTO event_consumer_dlq").
		WithArgs("orders.events", "1-0", `{"x":1}`, "boom", 3, "t", "c", ts).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := repo.InsertDLQ(context.Background(), "orders.events", "1-0", []byte(`{"x":1}`), "boom", 3, "t", "c", ts); err != nil {
		t.Fatalf("err=%v", err)
	}

	mock.ExpectExec("INSERT INTO order_event_projection").
		WithArgs(int64(1), "order.created", "paid", int64(10), ts).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := repo.UpsertOrderProjection(context.Background(), OrderCreatedEvent{OrderID: 1, PaymentStatus: "paid", TotalCents: 10}, ts); err != nil {
		t.Fatalf("err=%v", err)
	}
	if err := repo.UpsertOrderProjection(context.Background(), OrderCreatedEvent{OrderID: 0}, ts); err == nil {
		t.Fatal("expected invalid order id")
	}
}

func TestPostgresRepoErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewPostgresRepo(db)
	ts := time.Now()

	mock.ExpectQuery("SELECT id, event_type").WithArgs(ts, 1).WillReturnError(errors.New("qfail"))
	if _, err := repo.FetchPending(context.Background(), 1, ts); err == nil || !strings.Contains(err.Error(), "query outbox pending") {
		t.Fatalf("err=%v", err)
	}

	rows := sqlmock.NewRows([]string{"id", "event_type", "aggregate_type", "aggregate_id", "payload", "trace_id", "correlation_id", "attempts"}).
		AddRow("bad", "x", "x", int64(1), "{}", "t", "c", 0)
	mock.ExpectQuery("SELECT id, event_type").WithArgs(ts, 2).WillReturnRows(rows)
	if _, err := repo.FetchPending(context.Background(), 2, ts); err == nil || !strings.Contains(err.Error(), "scan outbox row") {
		t.Fatalf("err=%v", err)
	}

	rows = sqlmock.NewRows([]string{"id", "event_type", "aggregate_type", "aggregate_id", "payload", "trace_id", "correlation_id", "attempts"}).
		AddRow(int64(1), "x", "x", int64(1), "{}", "t", "c", 0).
		RowError(0, errors.New("iter fail"))
	mock.ExpectQuery("SELECT id, event_type").WithArgs(ts, 3).WillReturnRows(rows)
	if _, err := repo.FetchPending(context.Background(), 3, ts); err == nil || !strings.Contains(err.Error(), "iterate outbox rows") {
		t.Fatalf("err=%v", err)
	}

	mock.ExpectExec("UPDATE outbox_events").WillReturnError(errors.New("x"))
	if err := repo.MarkPublished(context.Background(), 1, ts); err == nil {
		t.Fatal("expected err")
	}
	mock.ExpectExec("UPDATE outbox_events").WillReturnError(errors.New("x"))
	if err := repo.MarkRetry(context.Background(), 1, 1, ts, "x"); err == nil {
		t.Fatal("expected err")
	}
	mock.ExpectExec("UPDATE outbox_events").WillReturnError(errors.New("x"))
	if err := repo.MarkDead(context.Background(), 1, 1, "x"); err == nil {
		t.Fatal("expected err")
	}
	mock.ExpectExec("INSERT INTO event_consumer_dlq").WillReturnError(errors.New("x"))
	if err := repo.InsertDLQ(context.Background(), "", "", nil, "", 0, "", "", ts); err == nil {
		t.Fatal("expected err")
	}
	mock.ExpectExec("INSERT INTO order_event_projection").WillReturnError(errors.New("x"))
	if err := repo.UpsertOrderProjection(context.Background(), OrderCreatedEvent{OrderID: 1}, ts); err == nil {
		t.Fatal("expected err")
	}

	_ = sql.ErrNoRows
}
