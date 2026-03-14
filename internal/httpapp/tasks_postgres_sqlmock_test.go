package httpapp

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNewPostgresHandlerSuccessAndClose(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	restorePostgresRepoFns(t)
	h, closeFn, err := NewPostgresHandler(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("NewPostgresHandler error: %v", err)
	}
	if h == nil || closeFn == nil {
		t.Fatalf("handler nil? %v closeFn nil? %v", h == nil, closeFn == nil)
	}
	if err := closeFn(); err != nil {
		t.Fatalf("closeFn error: %v", err)
	}
}

func TestNewPostgresHandlerError(t *testing.T) {
	restorePostgresRepoFns(t)
	sqlOpenFn = func(string, string) (*sql.DB, error) {
		return nil, errors.New("open failed")
	}

	h, closeFn, err := NewPostgresHandler(context.Background(), "postgres://bad")
	if err == nil || !strings.Contains(err.Error(), "open postgres connection") {
		t.Fatalf("err = %v", err)
	}
	if h != nil || closeFn != nil {
		t.Fatalf("expected nil handler/closeFn on error")
	}
}

func TestRunMigrationsBranchesWithSQLMock(t *testing.T) {
	t.Run("advisory lock error", func(t *testing.T) {
		restorePostgresRepoFns(t)
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		repo := &postgresTaskRepository{db: db}
		mock.ExpectExec("SELECT pg_advisory_lock").WithArgs(migrationsAdvisoryLockKey).WillReturnError(errors.New("lock failed"))

		err = repo.runMigrations(context.Background())
		if err == nil || !strings.Contains(err.Error(), "acquire migrations advisory lock") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("skip dir and empty file", func(t *testing.T) {
		restorePostgresRepoFns(t)
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		repo := &postgresTaskRepository{db: db}
		expectMigrationLock(mock)
		readDirMigrationsFn = func(fs.FS, string) ([]fs.DirEntry, error) {
			return []fs.DirEntry{
				fakeMigrationDirEntry{name: "subdir", isDir: true},
				fakeMigrationDirEntry{name: "001.sql", isDir: false},
			}, nil
		}
		readFileMigrationsFn = func(fs.FS, string) ([]byte, error) { return []byte("   "), nil }

		if err := repo.runMigrations(context.Background()); err != nil {
			t.Fatalf("runMigrations error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("expectations: %v", err)
		}
	})

	t.Run("read file error", func(t *testing.T) {
		restorePostgresRepoFns(t)
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		repo := &postgresTaskRepository{db: db}
		expectMigrationLock(mock)
		readDirMigrationsFn = func(fs.FS, string) ([]fs.DirEntry, error) {
			return []fs.DirEntry{fakeMigrationDirEntry{name: "001.sql"}}, nil
		}
		readFileMigrationsFn = func(fs.FS, string) ([]byte, error) { return nil, errors.New("read file failed") }

		err = repo.runMigrations(context.Background())
		if err == nil || !strings.Contains(err.Error(), "read migration 001.sql") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("apply migration error", func(t *testing.T) {
		restorePostgresRepoFns(t)
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		repo := &postgresTaskRepository{db: db}
		expectMigrationLock(mock)
		readDirMigrationsFn = func(fs.FS, string) ([]fs.DirEntry, error) {
			return []fs.DirEntry{fakeMigrationDirEntry{name: "001.sql"}}, nil
		}
		readFileMigrationsFn = func(fs.FS, string) ([]byte, error) { return []byte("CREATE TABLE x();"), nil }
		mock.ExpectExec("CREATE TABLE x").WillReturnError(errors.New("exec failed"))

		err = repo.runMigrations(context.Background())
		if err == nil || !strings.Contains(err.Error(), "apply migration 001.sql") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPostgresListAndDeleteErrorBranchesWithSQLMock(t *testing.T) {
	t.Run("list scan row error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		rows := sqlmock.NewRows([]string{"id", "owner_user_id", "title", "note", "done", "created_at", "updated_at"}).
			AddRow("bad-id", int64(1), "t", "", false, "2026-02-26", "2026-02-26")
		mock.ExpectQuery("SELECT id, owner_user_id, title, note, done, created_at, updated_at").WillReturnRows(rows)

		if _, err := repo.List(context.Background()); err == nil || !strings.Contains(err.Error(), "scan task row") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("list rows iteration error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		rows := sqlmock.NewRows([]string{"id", "owner_user_id", "title", "note", "done", "created_at", "updated_at"}).
			AddRow(int64(1), int64(1), "t", "", false, "2026-02-26T00:00:00Z", "2026-02-26T00:00:00Z").
			RowError(0, errors.New("row iter failed"))
		mock.ExpectQuery("SELECT id, owner_user_id, title, note, done, created_at, updated_at").WillReturnRows(rows)

		if _, err := repo.List(context.Background()); err == nil || !strings.Contains(err.Error(), "iterate task rows") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("delete rows affected error", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		mock.ExpectExec("DELETE FROM tasks").WithArgs(int64(1)).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err = repo.Delete(context.Background(), 1)
		if err == nil || !strings.Contains(err.Error(), "rows affected") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("insert short url unique violation", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		mock.ExpectQuery("INSERT INTO short_urls").
			WithArgs("dup1", "https://example.com", sqlmock.AnyArg()).
			WillReturnError(&pgconn.PgError{Code: "23505"})

		_, err = repo.CreateShortURL(context.Background(), createShortURLRepositoryInput{
			Code:      "dup1",
			TargetURL: "https://example.com",
		}, mustParseRFC3339(t, "2026-03-13T10:00:00Z"))
		if !errors.Is(err, errShortURLCodeConflict) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("get short url not found", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		mock.ExpectQuery("SELECT id, code, target_url, created_at").
			WithArgs("missing").
			WillReturnError(sql.ErrNoRows)

		_, err = repo.GetShortURLByCode(context.Background(), "missing")
		if !errors.Is(err, errShortURLNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("create file and get file branches", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		ts := mustParseRFC3339(t, "2026-03-14T10:00:00Z")

		rows := sqlmock.NewRows([]string{
			"id", "owner_user_id", "file_name", "content_type", "size_bytes", "sha256", "storage_provider", "storage_key", "created_at",
		}).AddRow(int64(1), int64(7), "doc.txt", "text/plain", int64(12), strings.Repeat("a", 64), "disk", "k1", ts)
		mock.ExpectQuery("INSERT INTO uploaded_files").
			WithArgs(int64(7), "doc.txt", "text/plain", int64(12), strings.Repeat("a", 64), "disk", "k1", ts).
			WillReturnRows(rows)

		created, err := repo.CreateFile(context.Background(), createFileInput{
			OwnerUserID:     7,
			FileName:        "doc.txt",
			ContentType:     "text/plain",
			SizeBytes:       12,
			SHA256:          strings.Repeat("a", 64),
			StorageProvider: "disk",
			StorageKey:      "k1",
		}, ts)
		if err != nil {
			t.Fatalf("CreateFile err: %v", err)
		}
		if created.ID != 1 || created.OwnerUserID != 7 {
			t.Fatalf("created=%+v", created)
		}

		getRows := sqlmock.NewRows([]string{
			"id", "owner_user_id", "file_name", "content_type", "size_bytes", "sha256", "storage_provider", "storage_key", "created_at",
		}).AddRow(int64(1), int64(7), "doc.txt", "text/plain", int64(12), strings.Repeat("a", 64), "disk", "k1", ts)
		mock.ExpectQuery("SELECT id, owner_user_id, file_name, content_type, size_bytes, sha256, storage_provider, storage_key, created_at").
			WithArgs(int64(1)).
			WillReturnRows(getRows)
		got, err := repo.GetFile(context.Background(), 1)
		if err != nil {
			t.Fatalf("GetFile err: %v", err)
		}
		if got.ID != 1 {
			t.Fatalf("got=%+v", got)
		}

		mock.ExpectQuery("SELECT id, owner_user_id, file_name, content_type, size_bytes, sha256, storage_provider, storage_key, created_at").
			WithArgs(int64(2)).
			WillReturnError(sql.ErrNoRows)
		if _, err := repo.GetFile(context.Background(), 2); !errors.Is(err, errFileNotFound) {
			t.Fatalf("err=%v", err)
		}

		mock.ExpectQuery("INSERT INTO uploaded_files").
			WillReturnError(errors.New("insert fail"))
		if _, err := repo.CreateFile(context.Background(), createFileInput{}, ts); err == nil || !strings.Contains(err.Error(), "insert uploaded file") {
			t.Fatalf("err=%v", err)
		}

		mock.ExpectQuery("SELECT id, owner_user_id, file_name, content_type, size_bytes, sha256, storage_provider, storage_key, created_at").
			WithArgs(int64(3)).
			WillReturnError(errors.New("select fail"))
		if _, err := repo.GetFile(context.Background(), 3); err == nil || !strings.Contains(err.Error(), "select uploaded file") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("list products scan and rows error branches", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		rows := sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
			AddRow("bad-id", "A", int64(100), int64(1), "2026-03-14", "2026-03-14")
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").WillReturnRows(rows)
		if _, err := repo.ListProducts(context.Background()); err == nil || !strings.Contains(err.Error(), "scan product row") {
			t.Fatalf("err=%v", err)
		}

		rows = sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
			AddRow(int64(1), "A", int64(100), int64(1), "2026-03-14T00:00:00Z", "2026-03-14T00:00:00Z").
			RowError(0, errors.New("row fail"))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").WillReturnRows(rows)
		if _, err := repo.ListProducts(context.Background()); err == nil || !strings.Contains(err.Error(), "iterate product rows") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("get order query branches", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}

		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnError(sql.ErrNoRows)
		if _, err := repo.GetOrder(context.Background(), 1); !errors.Is(err, errOrderNotFound) {
			t.Fatalf("err=%v", err)
		}

		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(2)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status", "total_cents", "idempotency_key", "created_at", "updated_at"}).
				AddRow(int64(2), int64(7), "paid", int64(100), "k", mustParseRFC3339(t, "2026-03-14T10:00:00Z"), mustParseRFC3339(t, "2026-03-14T10:00:00Z")))
		mock.ExpectQuery("SELECT product_id, quantity, unit_price_cents, line_total_cents").
			WithArgs(int64(2)).
			WillReturnError(errors.New("items query fail"))
		if _, err := repo.GetOrder(context.Background(), 2); err == nil || !strings.Contains(err.Error(), "query order items") {
			t.Fatalf("err=%v", err)
		}

		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(3)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status", "total_cents", "idempotency_key", "created_at", "updated_at"}).
				AddRow(int64(3), int64(7), "paid", int64(100), "k", mustParseRFC3339(t, "2026-03-14T10:00:00Z"), mustParseRFC3339(t, "2026-03-14T10:00:00Z")))
		mock.ExpectQuery("SELECT product_id, quantity, unit_price_cents, line_total_cents").
			WithArgs(int64(3)).
			WillReturnRows(sqlmock.NewRows([]string{"product_id", "quantity", "unit_price_cents", "line_total_cents"}).
				AddRow("bad", int64(1), int64(100), int64(100)))
		if _, err := repo.GetOrder(context.Background(), 3); err == nil || !strings.Contains(err.Error(), "scan order item row") {
			t.Fatalf("err=%v", err)
		}

		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(4)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status", "total_cents", "idempotency_key", "created_at", "updated_at"}).
				AddRow(int64(4), int64(7), "paid", int64(100), "k", mustParseRFC3339(t, "2026-03-14T10:00:00Z"), mustParseRFC3339(t, "2026-03-14T10:00:00Z")))
		mock.ExpectQuery("SELECT product_id, quantity, unit_price_cents, line_total_cents").
			WithArgs(int64(4)).
			WillReturnRows(sqlmock.NewRows([]string{"product_id", "quantity", "unit_price_cents", "line_total_cents"}).
				AddRow(int64(1), int64(1), int64(100), int64(100)).
				RowError(0, errors.New("rows err")))
		if _, err := repo.GetOrder(context.Background(), 4); err == nil || !strings.Contains(err.Error(), "iterate order item rows") {
			t.Fatalf("err=%v", err)
		}

		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(5)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status", "total_cents", "idempotency_key", "created_at", "updated_at"}).
				AddRow(int64(5), int64(7), "paid", int64(100), "k", mustParseRFC3339(t, "2026-03-14T10:00:00Z"), mustParseRFC3339(t, "2026-03-14T10:00:00Z")))
		mock.ExpectQuery("SELECT product_id, quantity, unit_price_cents, line_total_cents").
			WithArgs(int64(5)).
			WillReturnRows(sqlmock.NewRows([]string{"product_id", "quantity", "unit_price_cents", "line_total_cents"}))
		mock.ExpectQuery("SELECT status, provider_txn_id").
			WithArgs(int64(5)).
			WillReturnError(errors.New("payment read fail"))
		if _, err := repo.GetOrder(context.Background(), 5); err == nil || !strings.Contains(err.Error(), "select payment transaction") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("create order conflict and payment error branches", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}
		ts := mustParseRFC3339(t, "2026-03-14T10:00:00Z")

		// Existing key without response_order_id => conflict.
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").
			WithArgs(int64(7), "k", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(false, nil))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "k", createOrderInput{
			Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}},
		}, mockPaymentProvider{}, ts); !errors.Is(err, errIdempotencyKeyExists) {
			t.Fatalf("err=%v", err)
		}

		// Payment provider error.
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").
			WithArgs(int64(7), "k2", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "k2", createOrderInput{
			Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}},
		}, badPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "charge payment") {
			t.Fatalf("err=%v", err)
		}
	})

	t.Run("create order other error branches", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()
		repo := &postgresTaskRepository{db: db}
		ts := mustParseRFC3339(t, "2026-03-14T10:00:00Z")

		// idempotency insert query error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "a", ts).WillReturnError(errors.New("idem insert fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "a", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "insert idempotency key") {
			t.Fatalf("err=%v", err)
		}

		// existing order id path with getOrderTx error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "b", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(false, int64(11)))
		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(11)).
			WillReturnError(errors.New("select order fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "b", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "select order") {
			t.Fatalf("err=%v", err)
		}

		// existing order id path with commit error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "c", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(false, int64(12)))
		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(12)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status", "total_cents", "idempotency_key", "created_at", "updated_at"}).
				AddRow(int64(12), int64(7), "paid", int64(100), "c", ts, ts))
		mock.ExpectQuery("SELECT product_id, quantity, unit_price_cents, line_total_cents").
			WithArgs(int64(12)).
			WillReturnRows(sqlmock.NewRows([]string{"product_id", "quantity", "unit_price_cents", "line_total_cents"}))
		mock.ExpectQuery("SELECT status, provider_txn_id").
			WithArgs(int64(12)).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectCommit().WillReturnError(errors.New("commit fail"))
		if _, err := repo.CreateOrder(context.Background(), 7, "c", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "commit tx") {
			t.Fatalf("err=%v", err)
		}

		// product select generic error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "d", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnError(errors.New("product query fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "d", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "select product for update") {
			t.Fatalf("err=%v", err)
		}

		// insert order error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "e", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").WillReturnError(errors.New("insert order fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "e", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "insert order") {
			t.Fatalf("err=%v", err)
		}

		// insert order item error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "f", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(33)))
		mock.ExpectExec("INSERT INTO order_items").WillReturnError(errors.New("insert item fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "f", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "insert order item") {
			t.Fatalf("err=%v", err)
		}

		// stock update error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "g", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(34)))
		mock.ExpectExec("INSERT INTO order_items").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE products SET stock_qty").WillReturnError(errors.New("stock fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "g", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "update product stock") {
			t.Fatalf("err=%v", err)
		}

		// payment insert error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "h", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(35)))
		mock.ExpectExec("INSERT INTO order_items").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE products SET stock_qty").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO payment_transactions").WillReturnError(errors.New("payment insert fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "h", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "insert payment transaction") {
			t.Fatalf("err=%v", err)
		}

		// idempotency update error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "i", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(36)))
		mock.ExpectExec("INSERT INTO order_items").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE products SET stock_qty").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO payment_transactions").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE idempotency_keys SET response_order_id").WillReturnError(errors.New("idem update fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "i", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "update idempotency key response order") {
			t.Fatalf("err=%v", err)
		}

		// getOrderTx error after successful writes
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "j", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(37)))
		mock.ExpectExec("INSERT INTO order_items").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE products SET stock_qty").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO payment_transactions").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE idempotency_keys SET response_order_id").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO outbox_events").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(37)).
			WillReturnError(errors.New("reload order fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "j", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "select order") {
			t.Fatalf("err=%v", err)
		}

		// commit error after successful reload
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "k", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(38)))
		mock.ExpectExec("INSERT INTO order_items").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE products SET stock_qty").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO payment_transactions").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE idempotency_keys SET response_order_id").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO outbox_events").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectQuery("SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at").
			WithArgs(int64(38)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "status", "total_cents", "idempotency_key", "created_at", "updated_at"}).
				AddRow(int64(38), int64(7), "paid", int64(100), "k", ts, ts))
		mock.ExpectQuery("SELECT product_id, quantity, unit_price_cents, line_total_cents").
			WithArgs(int64(38)).
			WillReturnRows(sqlmock.NewRows([]string{"product_id", "quantity", "unit_price_cents", "line_total_cents"}).
				AddRow(int64(1), int64(1), int64(100), int64(100)))
		mock.ExpectQuery("SELECT status, provider_txn_id").
			WithArgs(int64(38)).
			WillReturnRows(sqlmock.NewRows([]string{"status", "provider_txn_id"}).AddRow("paid", "ref"))
		mock.ExpectCommit().WillReturnError(errors.New("commit fail final"))
		if _, err := repo.CreateOrder(context.Background(), 7, "k", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "commit tx") {
			t.Fatalf("err=%v", err)
		}

		// outbox insert error
		mock.ExpectBegin()
		mock.ExpectQuery("WITH ins AS").WithArgs(int64(7), "l", ts).
			WillReturnRows(sqlmock.NewRows([]string{"inserted", "response_order_id"}).AddRow(true, nil))
		mock.ExpectQuery("SELECT id, name, price_cents, stock_qty, created_at, updated_at").
			WithArgs(int64(1)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "name", "price_cents", "stock_qty", "created_at", "updated_at"}).
				AddRow(int64(1), "A", int64(100), int64(10), ts, ts))
		mock.ExpectQuery("INSERT INTO orders").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(39)))
		mock.ExpectExec("INSERT INTO order_items").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE products SET stock_qty").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO payment_transactions").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("UPDATE idempotency_keys SET response_order_id").WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec("INSERT INTO outbox_events").WillReturnError(errors.New("outbox fail"))
		mock.ExpectRollback()
		if _, err := repo.CreateOrder(context.Background(), 7, "l", createOrderInput{Items: []createOrderItemInput{{ProductID: 1, Quantity: 1}}}, mockPaymentProvider{}, ts); err == nil || !strings.Contains(err.Error(), "insert outbox event") {
			t.Fatalf("err=%v", err)
		}
	})
}

type fakeMigrationDirEntry struct {
	name  string
	isDir bool
}

func (f fakeMigrationDirEntry) Name() string { return f.name }
func (f fakeMigrationDirEntry) IsDir() bool  { return f.isDir }
func (f fakeMigrationDirEntry) Type() fs.FileMode {
	if f.isDir {
		return fs.ModeDir
	}
	return 0
}
func (f fakeMigrationDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("not implemented") }

func mustParseRFC3339(t *testing.T, value string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("time.Parse error: %v", err)
	}
	return ts
}

func expectMigrationLock(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SELECT pg_advisory_lock").WithArgs(migrationsAdvisoryLockKey).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("SELECT pg_advisory_unlock").WithArgs(migrationsAdvisoryLockKey).WillReturnResult(sqlmock.NewResult(0, 1))
}
