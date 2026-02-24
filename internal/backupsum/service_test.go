package backupsum

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCopiesFilesComputesChecksumsAndWritesReports(t *testing.T) {
	restorePackageFns(t)

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "backup")
	reportFile := filepath.Join(t.TempDir(), "report.json")

	mustWriteFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	mustWriteFile(t, filepath.Join(src, "nested", "b.txt"), "beta", 0o600)

	var stdout bytes.Buffer
	report, err := Run(Config{
		SourceDir:      src,
		DestinationDir: dst,
		ReportPath:     reportFile,
	}, &stdout)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if report.Summary.FileCount != 2 {
		t.Fatalf("file_count = %d, want 2", report.Summary.FileCount)
	}
	if report.Summary.TotalBytes != int64(len("alpha")+len("beta")) {
		t.Fatalf("total_bytes = %d", report.Summary.TotalBytes)
	}
	if len(report.Files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(report.Files))
	}

	checkCopiedFile(t, filepath.Join(dst, "a.txt"), "alpha", 0o644)
	checkCopiedFile(t, filepath.Join(dst, "nested", "b.txt"), "beta", 0o600)

	stdoutReport := decodeReport(t, stdout.Bytes())
	fileReport := decodeReport(t, mustReadFile(t, reportFile))

	if stdoutReport.Summary != report.Summary {
		t.Fatalf("stdout summary mismatch: %+v vs %+v", stdoutReport.Summary, report.Summary)
	}
	if fileReport.Summary != report.Summary {
		t.Fatalf("file summary mismatch: %+v vs %+v", fileReport.Summary, report.Summary)
	}
	if stdoutReport.Files[0].RelativePath != "a.txt" || stdoutReport.Files[1].RelativePath != "nested/b.txt" {
		t.Fatalf("unexpected file order/paths: %+v", stdoutReport.Files)
	}
}

func TestRunValidationErrors(t *testing.T) {
	restorePackageFns(t)

	t.Run("missing source", func(t *testing.T) {
		_, err := Run(Config{DestinationDir: t.TempDir()}, &bytes.Buffer{})
		assertErrorContains(t, err, "source directory is required")
	})

	t.Run("missing destination", func(t *testing.T) {
		_, err := Run(Config{SourceDir: t.TempDir()}, &bytes.Buffer{})
		assertErrorContains(t, err, "destination directory is required")
	})

	t.Run("source not found", func(t *testing.T) {
		_, err := Run(Config{
			SourceDir:      filepath.Join(t.TempDir(), "missing"),
			DestinationDir: t.TempDir(),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "stat source directory")
	})

	t.Run("source is file", func(t *testing.T) {
		dir := t.TempDir()
		file := filepath.Join(dir, "file.txt")
		mustWriteFile(t, file, "x", 0o644)
		_, err := Run(Config{
			SourceDir:      file,
			DestinationDir: t.TempDir(),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "source path must be a directory")
	})

	t.Run("same source and destination", func(t *testing.T) {
		dir := t.TempDir()
		_, err := Run(Config{SourceDir: dir, DestinationDir: dir}, &bytes.Buffer{})
		assertErrorContains(t, err, "must be different")
	})

	t.Run("destination inside source", func(t *testing.T) {
		src := t.TempDir()
		_, err := Run(Config{
			SourceDir:      src,
			DestinationDir: filepath.Join(src, "backup"),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "cannot be inside source")
	})
}

func TestRunSkipsNonRegularFiles(t *testing.T) {
	restorePackageFns(t)

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "backup")
	mustWriteFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)

	linkPath := filepath.Join(src, "a-link.txt")
	if err := os.Symlink(filepath.Join(src, "a.txt"), linkPath); err != nil {
		t.Skipf("symlink unsupported on this system: %v", err)
	}

	var stdout bytes.Buffer
	report, err := Run(Config{SourceDir: src, DestinationDir: dst}, &stdout)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if report.Summary.FileCount != 1 {
		t.Fatalf("file_count = %d, want 1", report.Summary.FileCount)
	}
	if _, err := os.Lstat(filepath.Join(dst, "a-link.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink should not be copied, got err=%v", err)
	}
}

func TestRunWriteStdoutError(t *testing.T) {
	restorePackageFns(t)

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "backup")
	mustWriteFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)

	_, err := Run(Config{SourceDir: src, DestinationDir: dst}, failWriter{})
	assertErrorContains(t, err, "write stdout report")
}

func TestRunWriteReportFileError(t *testing.T) {
	restorePackageFns(t)

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "backup")
	mustWriteFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)

	reportPath := filepath.Join(t.TempDir(), "missing-dir", "report.json")
	_, err := Run(Config{
		SourceDir:      src,
		DestinationDir: dst,
		ReportPath:     reportPath,
	}, &bytes.Buffer{})
	assertErrorContains(t, err, "write report file")
}

func TestRunBackupStepError(t *testing.T) {
	restorePackageFns(t)

	src := t.TempDir()
	mustWriteFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)

	dstParent := t.TempDir()
	dstFile := filepath.Join(dstParent, "not-a-dir")
	mustWriteFile(t, dstFile, "x", 0o644)

	_, err := Run(Config{SourceDir: src, DestinationDir: dstFile}, &bytes.Buffer{})
	assertErrorContains(t, err, "create destination directory")
}

func TestCopyFileWithSHA256Errors(t *testing.T) {
	restorePackageFns(t)

	t.Run("open source", func(t *testing.T) {
		_, _, err := copyFileWithSHA256(filepath.Join(t.TempDir(), "missing.txt"), filepath.Join(t.TempDir(), "out.txt"), 0o644)
		assertErrorContains(t, err, "open source")
	})

	t.Run("open destination", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "a.txt")
		mustWriteFile(t, src, "alpha", 0o644)
		dstDir := t.TempDir()
		_, _, err := copyFileWithSHA256(src, dstDir, 0o644)
		assertErrorContains(t, err, "open destination")
	})
}

func TestRunMarshalError(t *testing.T) {
	restorePackageFns(t)

	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "backup")
	mustWriteFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)

	jsonMarshalFn = func(any, string, string) ([]byte, error) {
		return nil, errors.New("marshal failed")
	}

	_, err := Run(Config{SourceDir: src, DestinationDir: dst}, &bytes.Buffer{})
	assertErrorContains(t, err, "marshal report")
}

func TestValidateConfigInjectedErrors(t *testing.T) {
	restorePackageFns(t)

	t.Run("abs source", func(t *testing.T) {
		absPathFn = func(string) (string, error) { return "", errors.New("abs failed") }
		_, err := validateConfig(Config{SourceDir: "src", DestinationDir: "dst"})
		assertErrorContains(t, err, "resolve source directory")
	})

	t.Run("abs destination", func(t *testing.T) {
		absCalls := 0
		absPathFn = func(path string) (string, error) {
			absCalls++
			if absCalls == 2 {
				return "", errors.New("abs dst failed")
			}
			return path, nil
		}
		statFn = func(string) (fs.FileInfo, error) { return fakeFileInfo{dir: true}, nil }
		_, err := validateConfig(Config{SourceDir: "src", DestinationDir: "dst"})
		assertErrorContains(t, err, "resolve destination directory")
	})

	t.Run("rel compare", func(t *testing.T) {
		absPathFn = func(path string) (string, error) { return path, nil }
		statFn = func(string) (fs.FileInfo, error) { return fakeFileInfo{dir: true}, nil }
		relPathFn = func(base, target string) (string, error) {
			if base == "src" && target == "dst" {
				return "", errors.New("rel failed")
			}
			return filepath.Rel(base, target)
		}
		_, err := validateConfig(Config{SourceDir: "src", DestinationDir: "dst"})
		assertErrorContains(t, err, "compare source and destination directories")
	})
}

func TestBackupAndHashInjectedErrors(t *testing.T) {
	t.Run("create destination directory", func(t *testing.T) {
		restorePackageFns(t)
		mkdirAllFn = func(string, fs.FileMode) error { return errors.New("mkdir failed") }
		_, err := backupAndHash(Config{SourceDir: "src", DestinationDir: "dst"})
		assertErrorContains(t, err, "create destination directory")
	})

	t.Run("walk error", func(t *testing.T) {
		restorePackageFns(t)
		mkdirAllFn = os.MkdirAll
		walkDirFn = func(root string, fn fs.WalkDirFunc) error {
			return fn(root, nil, errors.New("walk failed"))
		}
		_, err := backupAndHash(Config{SourceDir: "src", DestinationDir: t.TempDir()})
		assertErrorContains(t, err, "walk source directory")
	})

	t.Run("relative path error", func(t *testing.T) {
		restorePackageFns(t)
		src := t.TempDir()
		mustWriteFile(t, filepath.Join(src, "a.txt"), "x", 0o644)

		origRel := relPathFn
		relPathFn = func(base, target string) (string, error) {
			if base == src && target != src {
				return "", errors.New("rel path failed")
			}
			return origRel(base, target)
		}

		_, err := backupAndHash(Config{SourceDir: src, DestinationDir: filepath.Join(t.TempDir(), "dst")})
		assertErrorContains(t, err, "relative path")
	})

	t.Run("create subdirectory error", func(t *testing.T) {
		restorePackageFns(t)
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
			t.Fatalf("mkdir nested: %v", err)
		}
		dstRoot := filepath.Join(t.TempDir(), "dst")
		if err := os.MkdirAll(dstRoot, 0o755); err != nil {
			t.Fatalf("mkdir dst: %v", err)
		}
		mustWriteFile(t, filepath.Join(dstRoot, "nested"), "conflict", 0o644)

		_, err := backupAndHash(Config{SourceDir: src, DestinationDir: dstRoot})
		assertErrorContains(t, err, "create destination subdirectory")
	})

	t.Run("file info error", func(t *testing.T) {
		restorePackageFns(t)
		dst := filepath.Join(t.TempDir(), "dst")
		walkDirFn = func(root string, fn fs.WalkDirFunc) error {
			if err := fn(root, nil, nil); err != nil {
				return err
			}
			return fn(filepath.Join(root, "a.txt"), fakeDirEntry{infoErr: errors.New("info failed")}, nil)
		}
		_, err := backupAndHash(Config{SourceDir: "src", DestinationDir: dst})
		assertErrorContains(t, err, "file info")
	})

	t.Run("copy file wrapper error", func(t *testing.T) {
		restorePackageFns(t)
		src := t.TempDir()
		mustWriteFile(t, filepath.Join(src, "a.txt"), "alpha", 0o644)
		dstRoot := filepath.Join(t.TempDir(), "dst")
		if err := os.MkdirAll(filepath.Join(dstRoot, "a.txt"), 0o755); err != nil {
			t.Fatalf("mkdir conflicting dst path: %v", err)
		}
		_, err := backupAndHash(Config{SourceDir: src, DestinationDir: dstRoot})
		assertErrorContains(t, err, "copy file \"a.txt\"")
	})
}

func TestCopyFileWithSHA256CopyBytesError(t *testing.T) {
	restorePackageFns(t)

	src := filepath.Join(t.TempDir(), "a.txt")
	mustWriteFile(t, src, "alpha", 0o644)
	dst := filepath.Join(t.TempDir(), "out.txt")

	copyFn = func(io.Writer, io.Reader) (int64, error) {
		return 0, errors.New("copy failed")
	}

	_, _, err := copyFileWithSHA256(src, dst, 0o644)
	assertErrorContains(t, err, "copy bytes")
}

func TestIsNestedPath(t *testing.T) {
	cases := map[string]bool{
		"":               false,
		".":              false,
		"..":             false,
		"../sibling":     false,
		"../../outside":  false,
		"nested":         true,
		"nested/child":   true,
		".hidden-folder": true,
		"..not-parent":   true,
	}

	for rel, want := range cases {
		if got := isNestedPath(rel); got != want {
			t.Fatalf("isNestedPath(%q) = %v, want %v", rel, got, want)
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func decodeReport(t *testing.T, data []byte) Report {
	t.Helper()

	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal report: %v\n%s", err, data)
	}
	return report
}

func mustWriteFile(t *testing.T, path, body string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
}

func checkCopiedFile(t *testing.T, path, want string, wantPerm fs.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read copied file: %v", err)
	}
	if string(data) != want {
		t.Fatalf("copied file content = %q, want %q", string(data), want)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat copied file: %v", err)
	}
	if info.Mode().Perm() != wantPerm {
		t.Fatalf("copied file perm = %o, want %o", info.Mode().Perm(), wantPerm)
	}
}

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not contain %q", err.Error(), want)
	}
}

func restorePackageFns(t *testing.T) {
	t.Helper()

	oldAbs := absPathFn
	oldRel := relPathFn
	oldStat := statFn
	oldMkdirAll := mkdirAllFn
	oldWalkDir := walkDirFn
	oldWriteFile := writeFileFn
	oldJSONMarshal := jsonMarshalFn
	oldOpenRead := openReadFn
	oldOpenWrite := openWriteFileFn
	oldCopy := copyFn

	t.Cleanup(func() {
		absPathFn = oldAbs
		relPathFn = oldRel
		statFn = oldStat
		mkdirAllFn = oldMkdirAll
		walkDirFn = oldWalkDir
		writeFileFn = oldWriteFile
		jsonMarshalFn = oldJSONMarshal
		openReadFn = oldOpenRead
		openWriteFileFn = oldOpenWrite
		copyFn = oldCopy
	})
}

type fakeFileInfo struct {
	dir bool
}

func (f fakeFileInfo) Name() string { return "fake" }
func (f fakeFileInfo) Size() int64  { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

type fakeDirEntry struct {
	info    fs.FileInfo
	infoErr error
}

func (f fakeDirEntry) Name() string               { return "fake" }
func (f fakeDirEntry) IsDir() bool                { return false }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return f.info, f.infoErr }
