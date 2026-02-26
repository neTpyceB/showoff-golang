package httpapp

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
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
	t.Run("skip dir and empty file", func(t *testing.T) {
		restorePostgresRepoFns(t)
		db, mock, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		repo := &postgresTaskRepository{db: db}
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
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatalf("sqlmock.New: %v", err)
		}
		defer db.Close()

		repo := &postgresTaskRepository{db: db}
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

		rows := sqlmock.NewRows([]string{"id", "title", "note", "done", "created_at", "updated_at"}).
			AddRow("bad-id", "t", "", false, "2026-02-26", "2026-02-26")
		mock.ExpectQuery("SELECT id, title, note, done, created_at, updated_at").WillReturnRows(rows)

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

		rows := sqlmock.NewRows([]string{"id", "title", "note", "done", "created_at", "updated_at"}).
			AddRow(int64(1), "t", "", false, "2026-02-26T00:00:00Z", "2026-02-26T00:00:00Z").
			RowError(0, errors.New("row iter failed"))
		mock.ExpectQuery("SELECT id, title, note, done, created_at, updated_at").WillReturnRows(rows)

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
