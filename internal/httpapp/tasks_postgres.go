package httpapp

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var postgresMigrationsFS embed.FS

var (
	sqlOpenFn            = sql.Open
	readDirMigrationsFn  = fs.ReadDir
	readFileMigrationsFn = fs.ReadFile
)

const migrationsAdvisoryLockKey int64 = 884412773

type postgresTaskRepository struct {
	db *sql.DB
}

func NewPostgresHandler(ctx context.Context, databaseURL string) (http.Handler, func() error, error) {
	repo, err := NewPostgresTaskRepository(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	return NewHandlerWithRepositories(repo, repo), repo.Close, nil
}

func NewPostgresTaskRepository(ctx context.Context, databaseURL string) (*postgresTaskRepository, error) {
	db, err := sqlOpenFn("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	repo := &postgresTaskRepository{db: db}
	if err := repo.runMigrations(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return repo, nil
}

func (r *postgresTaskRepository) Close() error {
	return r.db.Close()
}

func (r *postgresTaskRepository) runMigrations(ctx context.Context) error {
	if _, err := r.db.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationsAdvisoryLockKey); err != nil {
		return fmt.Errorf("acquire migrations advisory lock: %w", err)
	}
	defer func() {
		_, _ = r.db.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationsAdvisoryLockKey)
	}()

	entries, err := readDirMigrationsFn(postgresMigrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	slices.Sort(names)

	for _, name := range names {
		sqlBytes, err := readFileMigrationsFn(postgresMigrationsFS, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if strings.TrimSpace(string(sqlBytes)) == "" {
			continue
		}
		if _, err := r.db.ExecContext(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}

	return nil
}

func (r *postgresTaskRepository) List(ctx context.Context) ([]task, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, owner_user_id, title, note, done, created_at, updated_at
		FROM tasks
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	items := []task{}
	for rows.Next() {
		var t task
		if err := rows.Scan(&t.ID, &t.OwnerUserID, &t.Title, &t.Note, &t.Done, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}
		items = append(items, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task rows: %w", err)
	}
	return items, nil
}

func (r *postgresTaskRepository) Create(ctx context.Context, in taskInput, ts time.Time) (task, error) {
	var out task
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO tasks (owner_user_id, title, note, done, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, owner_user_id, title, note, done, created_at, updated_at
	`, in.OwnerUserID, in.Title, in.Note, in.Done, ts, ts).Scan(
		&out.ID, &out.OwnerUserID, &out.Title, &out.Note, &out.Done, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return task{}, fmt.Errorf("insert task: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) Get(ctx context.Context, id int64) (task, error) {
	var out task
	err := r.db.QueryRowContext(ctx, `
		SELECT id, owner_user_id, title, note, done, created_at, updated_at
		FROM tasks
		WHERE id = $1
	`, id).Scan(&out.ID, &out.OwnerUserID, &out.Title, &out.Note, &out.Done, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return task{}, errTaskNotFound
		}
		return task{}, fmt.Errorf("select task: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) Update(ctx context.Context, id int64, in taskInput, ts time.Time) (task, error) {
	var out task
	err := r.db.QueryRowContext(ctx, `
		UPDATE tasks
		SET title = $2, note = $3, done = $4, updated_at = $5
		WHERE id = $1
		RETURNING id, owner_user_id, title, note, done, created_at, updated_at
	`, id, in.Title, in.Note, in.Done, ts).Scan(
		&out.ID, &out.OwnerUserID, &out.Title, &out.Note, &out.Done, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return task{}, errTaskNotFound
		}
		return task{}, fmt.Errorf("update task: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete task rows affected: %w", err)
	}
	if affected == 0 {
		return errTaskNotFound
	}
	return nil
}

func (r *postgresTaskRepository) CreateShortURL(ctx context.Context, in createShortURLRepositoryInput, ts time.Time) (shortURL, error) {
	var out shortURL
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO short_urls (code, target_url, created_at)
		VALUES ($1, $2, $3)
		RETURNING id, code, target_url, created_at
	`, in.Code, in.TargetURL, ts).Scan(
		&out.ID, &out.Code, &out.TargetURL, &out.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return shortURL{}, errShortURLCodeConflict
		}
		return shortURL{}, fmt.Errorf("insert short url: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) GetShortURLByCode(ctx context.Context, code string) (shortURL, error) {
	var out shortURL
	err := r.db.QueryRowContext(ctx, `
		SELECT id, code, target_url, created_at
		FROM short_urls
		WHERE code = $1
	`, code).Scan(&out.ID, &out.Code, &out.TargetURL, &out.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return shortURL{}, errShortURLNotFound
		}
		return shortURL{}, fmt.Errorf("select short url: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) CreateFile(ctx context.Context, in createFileInput, ts time.Time) (fileRecord, error) {
	var out fileRecord
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO uploaded_files (
			owner_user_id, file_name, content_type, size_bytes, sha256, storage_provider, storage_key, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, owner_user_id, file_name, content_type, size_bytes, sha256, storage_provider, storage_key, created_at
	`, in.OwnerUserID, in.FileName, in.ContentType, in.SizeBytes, in.SHA256, in.StorageProvider, in.StorageKey, ts).Scan(
		&out.ID, &out.OwnerUserID, &out.FileName, &out.ContentType, &out.SizeBytes,
		&out.SHA256, &out.StorageProvider, &out.StorageKey, &out.CreatedAt,
	)
	if err != nil {
		return fileRecord{}, fmt.Errorf("insert uploaded file: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) GetFile(ctx context.Context, id int64) (fileRecord, error) {
	var out fileRecord
	err := r.db.QueryRowContext(ctx, `
		SELECT id, owner_user_id, file_name, content_type, size_bytes, sha256, storage_provider, storage_key, created_at
		FROM uploaded_files
		WHERE id = $1
	`, id).Scan(
		&out.ID, &out.OwnerUserID, &out.FileName, &out.ContentType, &out.SizeBytes,
		&out.SHA256, &out.StorageProvider, &out.StorageKey, &out.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fileRecord{}, errFileNotFound
		}
		return fileRecord{}, fmt.Errorf("select uploaded file: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) CreateProduct(ctx context.Context, in createProductInput, ts time.Time) (product, error) {
	var out product
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO products (name, price_cents, stock_qty, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, price_cents, stock_qty, created_at, updated_at
	`, in.Name, in.PriceCents, in.StockQty, ts, ts).Scan(
		&out.ID, &out.Name, &out.PriceCents, &out.StockQty, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return product{}, fmt.Errorf("insert product: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) ListProducts(ctx context.Context) ([]product, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, price_cents, stock_qty, created_at, updated_at
		FROM products
		ORDER BY id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query products: %w", err)
	}
	defer rows.Close()
	out := []product{}
	for rows.Next() {
		var p product
		if err := rows.Scan(&p.ID, &p.Name, &p.PriceCents, &p.StockQty, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan product row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate product rows: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) CreateOrder(ctx context.Context, userID int64, idempotencyKey string, in createOrderInput, payments paymentProvider, ts time.Time) (order, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return order{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var inserted bool
	var existingOrderID sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		WITH ins AS (
			INSERT INTO idempotency_keys (user_id, idempotency_key, created_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (user_id, idempotency_key) DO NOTHING
			RETURNING true AS inserted, response_order_id
		)
		SELECT inserted, response_order_id FROM ins
		UNION ALL
		SELECT false AS inserted, response_order_id
		FROM idempotency_keys
		WHERE user_id = $1 AND idempotency_key = $2 AND NOT EXISTS (SELECT 1 FROM ins)
		LIMIT 1
	`, userID, idempotencyKey, ts).Scan(&inserted, &existingOrderID)
	if err != nil {
		return order{}, fmt.Errorf("insert idempotency key: %w", err)
	}
	if !inserted {
		if !existingOrderID.Valid {
			return order{}, errIdempotencyKeyExists
		}
		out, err := r.getOrderTx(ctx, tx, existingOrderID.Int64)
		if err != nil {
			return order{}, err
		}
		if err := tx.Commit(); err != nil {
			return order{}, fmt.Errorf("commit tx: %w", err)
		}
		return out, nil
	}

	total := int64(0)
	items := make([]orderItem, 0, len(in.Items))
	for _, it := range in.Items {
		if it.Quantity <= 0 {
			return order{}, errInvalidOrder
		}
		var p product
		err := tx.QueryRowContext(ctx, `
			SELECT id, name, price_cents, stock_qty, created_at, updated_at
			FROM products
			WHERE id = $1
			FOR UPDATE
		`, it.ProductID).Scan(&p.ID, &p.Name, &p.PriceCents, &p.StockQty, &p.CreatedAt, &p.UpdatedAt)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return order{}, errProductNotFound
			}
			return order{}, fmt.Errorf("select product for update: %w", err)
		}
		if p.StockQty < it.Quantity {
			return order{}, errInsufficientStock
		}
		line := p.PriceCents * it.Quantity
		total += line
		items = append(items, orderItem{
			ProductID:      p.ID,
			Quantity:       it.Quantity,
			UnitPriceCents: p.PriceCents,
			LineTotalCents: line,
		})
	}

	payment, err := payments.Charge(ctx, total, in.PaymentMethod)
	if err != nil {
		return order{}, fmt.Errorf("charge payment: %w", err)
	}
	orderStatus := "paid"
	if payment.Status != "paid" {
		orderStatus = "payment_failed"
	}

	var orderID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO orders (user_id, status, total_cents, idempotency_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id
	`, userID, orderStatus, total, idempotencyKey, ts, ts).Scan(&orderID); err != nil {
		return order{}, fmt.Errorf("insert order: %w", err)
	}

	for _, it := range items {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO order_items (order_id, product_id, quantity, unit_price_cents, line_total_cents)
			VALUES ($1, $2, $3, $4, $5)
		`, orderID, it.ProductID, it.Quantity, it.UnitPriceCents, it.LineTotalCents); err != nil {
			return order{}, fmt.Errorf("insert order item: %w", err)
		}
		if orderStatus == "paid" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE products SET stock_qty = stock_qty - $2, updated_at = $3 WHERE id = $1
			`, it.ProductID, it.Quantity, ts); err != nil {
				return order{}, fmt.Errorf("update product stock: %w", err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO payment_transactions (order_id, provider, status, provider_txn_id, amount_cents, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, orderID, "mock", payment.Status, payment.Ref, total, ts); err != nil {
		return order{}, fmt.Errorf("insert payment transaction: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE idempotency_keys SET response_order_id = $3 WHERE user_id = $1 AND idempotency_key = $2
	`, userID, idempotencyKey, orderID); err != nil {
		return order{}, fmt.Errorf("update idempotency key response order: %w", err)
	}

	out, err := r.getOrderTx(ctx, tx, orderID)
	if err != nil {
		return order{}, err
	}
	if err := tx.Commit(); err != nil {
		return order{}, fmt.Errorf("commit tx: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) GetOrder(ctx context.Context, id int64) (order, error) {
	return r.getOrderQuery(ctx, r.db, id)
}

type orderQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (r *postgresTaskRepository) getOrderTx(ctx context.Context, q orderQueryer, id int64) (order, error) {
	return r.getOrderQuery(ctx, q, id)
}

func (r *postgresTaskRepository) getOrderQuery(ctx context.Context, q orderQueryer, id int64) (order, error) {
	var out order
	err := q.QueryRowContext(ctx, `
		SELECT id, user_id, status, total_cents, idempotency_key, created_at, updated_at
		FROM orders
		WHERE id = $1
	`, id).Scan(
		&out.ID, &out.UserID, &out.Status, &out.TotalCents, &out.IdempotencyKey, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return order{}, errOrderNotFound
		}
		return order{}, fmt.Errorf("select order: %w", err)
	}
	rows, err := q.QueryContext(ctx, `
		SELECT product_id, quantity, unit_price_cents, line_total_cents
		FROM order_items
		WHERE order_id = $1
		ORDER BY id ASC
	`, id)
	if err != nil {
		return order{}, fmt.Errorf("query order items: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it orderItem
		if err := rows.Scan(&it.ProductID, &it.Quantity, &it.UnitPriceCents, &it.LineTotalCents); err != nil {
			return order{}, fmt.Errorf("scan order item row: %w", err)
		}
		out.Items = append(out.Items, it)
	}
	if err := rows.Err(); err != nil {
		return order{}, fmt.Errorf("iterate order item rows: %w", err)
	}
	var payStatus, payRef string
	if err := q.QueryRowContext(ctx, `
		SELECT status, provider_txn_id
		FROM payment_transactions
		WHERE order_id = $1
		ORDER BY id DESC
		LIMIT 1
	`, id).Scan(&payStatus, &payRef); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return order{}, fmt.Errorf("select payment transaction: %w", err)
		}
	} else {
		out.PaymentStatus = payStatus
		out.PaymentRef = payRef
	}
	return out, nil
}
