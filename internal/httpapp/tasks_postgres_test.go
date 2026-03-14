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
)

func TestPostgresTaskRepositoryIntegrationCRUDAndMigrations(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	restorePostgresRepoFns(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo, err := NewPostgresTaskRepository(ctx, dbURL)
	if err != nil {
		t.Fatalf("NewPostgresTaskRepository error: %v", err)
	}
	defer repo.Close()

	if _, err := repo.db.ExecContext(ctx, `TRUNCATE TABLE tasks RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate tasks: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx, `TRUNCATE TABLE short_urls RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate short_urls: %v", err)
	}
	if _, err := repo.db.ExecContext(ctx, `TRUNCATE TABLE payment_transactions, order_items, orders, idempotency_keys, products RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate ecommerce tables: %v", err)
	}

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected empty list, got %d", len(items))
	}

	ts1 := time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)
	created1, err := repo.Create(ctx, taskInput{OwnerUserID: 1, Title: "Task 1", Note: "n1", Done: false}, ts1)
	if err != nil {
		t.Fatalf("Create1 error: %v", err)
	}
	ts2 := ts1.Add(time.Minute)
	created2, err := repo.Create(ctx, taskInput{OwnerUserID: 1, Title: "Task 2", Note: "n2", Done: true}, ts2)
	if err != nil {
		t.Fatalf("Create2 error: %v", err)
	}
	if created1.ID != 1 || created2.ID != 2 {
		t.Fatalf("unexpected IDs: %d %d", created1.ID, created2.ID)
	}

	got1, err := repo.Get(ctx, created1.ID)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	if got1.Title != "Task 1" || got1.Note != "n1" || got1.Done {
		t.Fatalf("unexpected task: %+v", got1)
	}
	if got1.OwnerUserID != 1 {
		t.Fatalf("unexpected owner: %+v", got1)
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(all) != 2 || all[0].ID != 1 || all[1].ID != 2 {
		t.Fatalf("list result: %+v", all)
	}

	updated, err := repo.Update(ctx, created1.ID, taskInput{Title: "Task 1 updated", Note: "n1u", Done: true}, ts1.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("Update error: %v", err)
	}
	if updated.CreatedAt.UTC().Format(time.RFC3339) != ts1.Format(time.RFC3339) {
		t.Fatalf("created_at changed: %+v", updated)
	}
	if updated.UpdatedAt.UTC().Format(time.RFC3339) != ts1.Add(10*time.Minute).Format(time.RFC3339) {
		t.Fatalf("updated_at wrong: %+v", updated)
	}

	if err := repo.Delete(ctx, created2.ID); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	if _, err := repo.Get(ctx, created2.ID); !errors.Is(err, errTaskNotFound) {
		t.Fatalf("Get deleted err = %v", err)
	}
	if _, err := repo.Update(ctx, 999, taskInput{Title: "x"}, time.Now()); !errors.Is(err, errTaskNotFound) {
		t.Fatalf("Update missing err = %v", err)
	}
	if err := repo.Delete(ctx, 999); !errors.Is(err, errTaskNotFound) {
		t.Fatalf("Delete missing err = %v", err)
	}

	// Migrations are idempotent.
	if err := repo.runMigrations(ctx); err != nil {
		t.Fatalf("runMigrations second time error: %v", err)
	}

	shortCreated, err := repo.CreateShortURL(ctx, createShortURLRepositoryInput{
		Code:      "golang01",
		TargetURL: "https://go.dev/",
	}, ts1)
	if err != nil {
		t.Fatalf("CreateShortURL error: %v", err)
	}
	if shortCreated.ID != 1 || shortCreated.Code != "golang01" {
		t.Fatalf("short created = %+v", shortCreated)
	}

	gotShort, err := repo.GetShortURLByCode(ctx, "golang01")
	if err != nil {
		t.Fatalf("GetShortURLByCode error: %v", err)
	}
	if gotShort.TargetURL != "https://go.dev/" {
		t.Fatalf("got short url = %+v", gotShort)
	}

	if _, err := repo.CreateShortURL(ctx, createShortURLRepositoryInput{
		Code:      "golang01",
		TargetURL: "https://example.com/",
	}, ts1); !errors.Is(err, errShortURLCodeConflict) {
		t.Fatalf("duplicate short url err = %v", err)
	}
	if _, err := repo.GetShortURLByCode(ctx, "missing01"); !errors.Is(err, errShortURLNotFound) {
		t.Fatalf("missing short url err = %v", err)
	}

	// E-commerce happy path + idempotency.
	prod, err := repo.CreateProduct(ctx, createProductInput{Name: "Keyboard", PriceCents: 5000, StockQty: 5}, ts1)
	if err != nil {
		t.Fatalf("CreateProduct err: %v", err)
	}
	plist, err := repo.ListProducts(ctx)
	if err != nil || len(plist) == 0 {
		t.Fatalf("ListProducts err=%v items=%+v", err, plist)
	}
	o1, err := repo.CreateOrder(ctx, 99, "idem-1", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: prod.ID, Quantity: 2}},
		PaymentMethod: "pm_ok",
	}, mockPaymentProvider{}, ts1)
	if err != nil {
		t.Fatalf("CreateOrder err: %v", err)
	}
	if o1.Status != "paid" || o1.TotalCents != 10000 || len(o1.Items) != 1 {
		t.Fatalf("order=%+v", o1)
	}
	o2, err := repo.CreateOrder(ctx, 99, "idem-1", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: prod.ID, Quantity: 2}},
		PaymentMethod: "pm_ok",
	}, mockPaymentProvider{}, ts1)
	if err != nil || o2.ID != o1.ID {
		t.Fatalf("idempotency order=%+v err=%v", o2, err)
	}
	gotOrder, err := repo.GetOrder(ctx, o1.ID)
	if err != nil || gotOrder.ID != o1.ID {
		t.Fatalf("GetOrder=%+v err=%v", gotOrder, err)
	}

	oFail, err := repo.CreateOrder(ctx, 99, "idem-fail", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: prod.ID, Quantity: 1}},
		PaymentMethod: "pm_fail",
	}, mockPaymentProvider{}, ts1)
	if err != nil || oFail.Status != "payment_failed" {
		t.Fatalf("failed order=%+v err=%v", oFail, err)
	}
	if _, err := repo.GetOrder(ctx, 999999); !errors.Is(err, errOrderNotFound) {
		t.Fatalf("missing order err=%v", err)
	}
	if _, err := repo.CreateOrder(ctx, 99, "idem-bad-product", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: 999999, Quantity: 1}},
		PaymentMethod: "pm_ok",
	}, mockPaymentProvider{}, ts1); !errors.Is(err, errProductNotFound) {
		t.Fatalf("missing product err=%v", err)
	}
	if _, err := repo.CreateOrder(ctx, 99, "idem-bad-stock", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: prod.ID, Quantity: 9999}},
		PaymentMethod: "pm_ok",
	}, mockPaymentProvider{}, ts1); !errors.Is(err, errInsufficientStock) {
		t.Fatalf("stock err=%v", err)
	}
	if _, err := repo.CreateOrder(ctx, 99, "idem-bad-qty", createOrderInput{
		Items:         []createOrderItemInput{{ProductID: prod.ID, Quantity: 0}},
		PaymentMethod: "pm_ok",
	}, mockPaymentProvider{}, ts1); !errors.Is(err, errInvalidOrder) {
		t.Fatalf("qty err=%v", err)
	}
}

func TestPostgresTaskRepositoryConstructorAndErrorBranches(t *testing.T) {
	t.Run("open error", func(t *testing.T) {
		restorePostgresRepoFns(t)
		sqlOpenFn = func(driverName, dataSourceName string) (*sql.DB, error) {
			return nil, errors.New("open failed")
		}
		_, err := NewPostgresTaskRepository(context.Background(), "postgres://x")
		if err == nil || !strings.Contains(err.Error(), "open postgres connection") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("ping error", func(t *testing.T) {
		restorePostgresRepoFns(t)
		_, err := NewPostgresTaskRepository(context.Background(), "postgres://showoff:showoff@127.0.0.1:1/showoff?sslmode=disable")
		if err == nil || !strings.Contains(err.Error(), "ping postgres") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("migration read dir error", func(t *testing.T) {
		restorePostgresRepoFns(t)
		dbURL := os.Getenv("TEST_DATABASE_URL")
		if dbURL == "" {
			t.Skip("TEST_DATABASE_URL is not set")
		}
		readDirMigrationsFn = func(fsys fs.FS, name string) ([]fs.DirEntry, error) { // patch imports next
			return nil, errors.New("read dir failed")
		}
		_, err := NewPostgresTaskRepository(context.Background(), dbURL)
		if err == nil || !strings.Contains(err.Error(), "read migrations dir") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestPostgresTaskRepositoryMethodErrorBranches(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	restorePostgresRepoFns(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	repo, err := NewPostgresTaskRepository(ctx, dbURL)
	if err != nil {
		t.Fatalf("NewPostgresTaskRepository error: %v", err)
	}

	// Close DB to force query/exec failures.
	if err := repo.Close(); err != nil {
		t.Fatalf("close repo: %v", err)
	}

	if _, err := repo.List(ctx); err == nil || !strings.Contains(err.Error(), "query tasks") {
		t.Fatalf("List err = %v", err)
	}
	if _, err := repo.Create(ctx, taskInput{OwnerUserID: 1, Title: "x"}, time.Now()); err == nil || !strings.Contains(err.Error(), "insert task") {
		t.Fatalf("Create err = %v", err)
	}
	if _, err := repo.Get(ctx, 1); err == nil || !strings.Contains(err.Error(), "select task") {
		t.Fatalf("Get err = %v", err)
	}
	if _, err := repo.Update(ctx, 1, taskInput{Title: "x"}, time.Now()); err == nil || !strings.Contains(err.Error(), "update task") {
		t.Fatalf("Update err = %v", err)
	}
	if err := repo.Delete(ctx, 1); err == nil || !strings.Contains(err.Error(), "delete task") {
		t.Fatalf("Delete err = %v", err)
	}
	if _, err := repo.CreateShortURL(ctx, createShortURLRepositoryInput{Code: "a123", TargetURL: "https://example.com"}, time.Now()); err == nil || !strings.Contains(err.Error(), "insert short url") {
		t.Fatalf("CreateShortURL err = %v", err)
	}
	if _, err := repo.GetShortURLByCode(ctx, "a123"); err == nil || !strings.Contains(err.Error(), "select short url") {
		t.Fatalf("GetShortURLByCode err = %v", err)
	}
	if _, err := repo.CreateProduct(ctx, createProductInput{Name: "x", PriceCents: 1, StockQty: 1}, time.Now()); err == nil || !strings.Contains(err.Error(), "insert product") {
		t.Fatalf("CreateProduct err = %v", err)
	}
	if _, err := repo.ListProducts(ctx); err == nil || !strings.Contains(err.Error(), "query products") {
		t.Fatalf("ListProducts err = %v", err)
	}
	if _, err := repo.CreateOrder(ctx, 1, "x", createOrderInput{}, mockPaymentProvider{}, time.Now()); err == nil || !strings.Contains(err.Error(), "begin tx") {
		t.Fatalf("CreateOrder err = %v", err)
	}
	if _, err := repo.GetOrder(ctx, 1); err == nil || !strings.Contains(err.Error(), "select order") {
		t.Fatalf("GetOrder err = %v", err)
	}
}

func restorePostgresRepoFns(t *testing.T) {
	t.Helper()
	oldOpen := sqlOpenFn
	oldReadDir := readDirMigrationsFn
	oldReadFile := readFileMigrationsFn
	t.Cleanup(func() {
		sqlOpenFn = oldOpen
		readDirMigrationsFn = oldReadDir
		readFileMigrationsFn = oldReadFile
	})
}
