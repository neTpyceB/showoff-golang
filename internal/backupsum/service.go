package backupsum

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	SourceDir      string
	DestinationDir string
	ReportPath     string
}

type Report struct {
	SourceDir      string       `json:"source_dir"`
	DestinationDir string       `json:"destination_dir"`
	Files          []FileReport `json:"files"`
	Summary        Summary      `json:"summary"`
}

type FileReport struct {
	RelativePath string `json:"relative_path"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
}

type Summary struct {
	FileCount  int   `json:"file_count"`
	TotalBytes int64 `json:"total_bytes"`
}

var (
	absPathFn       = filepath.Abs
	relPathFn       = filepath.Rel
	statFn          = os.Stat
	mkdirAllFn      = os.MkdirAll
	walkDirFn       = filepath.WalkDir
	writeFileFn     = os.WriteFile
	jsonMarshalFn   = json.MarshalIndent
	openReadFn      = os.Open
	openWriteFileFn = os.OpenFile
	copyFn          = io.Copy
)

func Run(cfg Config, stdout io.Writer) (Report, error) {
	cleaned, err := validateConfig(cfg)
	if err != nil {
		return Report{}, err
	}

	report, err := backupAndHash(cleaned)
	if err != nil {
		return Report{}, err
	}

	data, err := jsonMarshalFn(report, "", "  ")
	if err != nil {
		return Report{}, fmt.Errorf("marshal report: %w", err)
	}

	if _, err := stdout.Write(append(data, '\n')); err != nil {
		return Report{}, fmt.Errorf("write stdout report: %w", err)
	}

	if cleaned.ReportPath != "" {
		if err := writeFileFn(cleaned.ReportPath, append(data, '\n'), 0o644); err != nil {
			return Report{}, fmt.Errorf("write report file: %w", err)
		}
	}

	return report, nil
}

func validateConfig(cfg Config) (Config, error) {
	if cfg.SourceDir == "" {
		return Config{}, errors.New("source directory is required")
	}
	if cfg.DestinationDir == "" {
		return Config{}, errors.New("destination directory is required")
	}

	srcAbs, err := absPathFn(cfg.SourceDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve source directory: %w", err)
	}
	dstAbs, err := absPathFn(cfg.DestinationDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve destination directory: %w", err)
	}

	srcInfo, err := statFn(srcAbs)
	if err != nil {
		return Config{}, fmt.Errorf("stat source directory: %w", err)
	}
	if !srcInfo.IsDir() {
		return Config{}, errors.New("source path must be a directory")
	}

	if srcAbs == dstAbs {
		return Config{}, errors.New("source and destination directories must be different")
	}

	rel, err := relPathFn(srcAbs, dstAbs)
	if err != nil {
		return Config{}, fmt.Errorf("compare source and destination directories: %w", err)
	}
	if isNestedPath(rel) {
		return Config{}, errors.New("destination directory cannot be inside source directory")
	}

	return Config{
		SourceDir:      srcAbs,
		DestinationDir: dstAbs,
		ReportPath:     cfg.ReportPath,
	}, nil
}

func backupAndHash(cfg Config) (Report, error) {
	if err := mkdirAllFn(cfg.DestinationDir, 0o755); err != nil {
		return Report{}, fmt.Errorf("create destination directory: %w", err)
	}

	report := Report{
		SourceDir:      cfg.SourceDir,
		DestinationDir: cfg.DestinationDir,
		Files:          make([]FileReport, 0),
	}

	err := walkDirFn(cfg.SourceDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk source directory: %w", walkErr)
		}

		relPath, err := relPathFn(cfg.SourceDir, path)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}
		if relPath == "." {
			return nil
		}

		dstPath := filepath.Join(cfg.DestinationDir, relPath)

		if d.IsDir() {
			if err := mkdirAllFn(dstPath, 0o755); err != nil {
				return fmt.Errorf("create destination subdirectory %q: %w", relPath, err)
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("file info %q: %w", relPath, err)
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		checksum, size, err := copyFileWithSHA256(path, dstPath, info.Mode().Perm())
		if err != nil {
			return fmt.Errorf("copy file %q: %w", relPath, err)
		}

		report.Files = append(report.Files, FileReport{
			RelativePath: filepath.ToSlash(relPath),
			SizeBytes:    size,
			SHA256:       checksum,
		})
		report.Summary.FileCount++
		report.Summary.TotalBytes += size
		return nil
	})
	if err != nil {
		return Report{}, err
	}

	return report, nil
}

func copyFileWithSHA256(srcPath, dstPath string, mode fs.FileMode) (string, int64, error) {
	srcFile, err := openReadFn(srcPath)
	if err != nil {
		return "", 0, fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := openWriteFileFn(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return "", 0, fmt.Errorf("open destination: %w", err)
	}
	defer dstFile.Close()

	hash := sha256.New()
	written, err := copyFn(io.MultiWriter(dstFile, hash), srcFile)
	if err != nil {
		return "", 0, fmt.Errorf("copy bytes: %w", err)
	}

	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func isNestedPath(rel string) bool {
	if rel == "" || rel == "." {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
