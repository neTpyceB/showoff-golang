package httpapp

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var postgresMigrationsFS embed.FS

var (
	sqlOpenFn            = sql.Open
	readDirMigrationsFn  = fs.ReadDir
	readFileMigrationsFn = fs.ReadFile
)

type postgresTaskRepository struct {
	db *sql.DB
}

func NewPostgresHandler(ctx context.Context, databaseURL string) (http.Handler, func() error, error) {
	repo, err := NewPostgresTaskRepository(ctx, databaseURL)
	if err != nil {
		return nil, nil, err
	}
	return NewHandlerWithTaskRepository(repo), repo.Close, nil
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
		SELECT id, title, note, done, created_at, updated_at
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
		if err := rows.Scan(&t.ID, &t.Title, &t.Note, &t.Done, &t.CreatedAt, &t.UpdatedAt); err != nil {
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
		INSERT INTO tasks (title, note, done, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, title, note, done, created_at, updated_at
	`, in.Title, in.Note, in.Done, ts, ts).Scan(
		&out.ID, &out.Title, &out.Note, &out.Done, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return task{}, fmt.Errorf("insert task: %w", err)
	}
	return out, nil
}

func (r *postgresTaskRepository) Get(ctx context.Context, id int64) (task, error) {
	var out task
	err := r.db.QueryRowContext(ctx, `
		SELECT id, title, note, done, created_at, updated_at
		FROM tasks
		WHERE id = $1
	`, id).Scan(&out.ID, &out.Title, &out.Note, &out.Done, &out.CreatedAt, &out.UpdatedAt)
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
		RETURNING id, title, note, done, created_at, updated_at
	`, id, in.Title, in.Note, in.Done, ts).Scan(
		&out.ID, &out.Title, &out.Note, &out.Done, &out.CreatedAt, &out.UpdatedAt,
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
