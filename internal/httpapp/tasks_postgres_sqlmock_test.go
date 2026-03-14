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
