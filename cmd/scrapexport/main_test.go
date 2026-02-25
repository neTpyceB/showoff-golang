package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"showoff-golang/internal/scrapexport"
)

func TestRunCLISuccess(t *testing.T) {
	restoreGlobals(t)

	var gotCfg scrapexport.Config
	runScraper = func(cfg scrapexport.Config, stdout io.Writer) (scrapexport.Report, error) {
		gotCfg = cfg
		_, _ = io.WriteString(stdout, "{\"ok\":true}\n")
		return scrapexport.Report{}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI([]string{
		"-url", "https://example.com/a",
		"-url", "https://example.com/b",
		"-json", "out.json",
		"-csv", "out.csv",
		"-timeout", "3s",
	}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.String() != "{\"ok\":true}\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if len(gotCfg.URLs) != 2 || gotCfg.URLs[0] != "https://example.com/a" || gotCfg.URLs[1] != "https://example.com/b" {
		t.Fatalf("urls = %#v", gotCfg.URLs)
	}
	if gotCfg.JSONPath != "out.json" || gotCfg.CSVPath != "out.csv" {
		t.Fatalf("paths = %+v", gotCfg)
	}
	if gotCfg.Timeout != 3*time.Second {
		t.Fatalf("timeout = %v", gotCfg.Timeout)
	}
}

func TestRunCLIParseError(t *testing.T) {
	restoreGlobals(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI([]string{"-timeout", "bad"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "invalid value") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCLIServiceError(t *testing.T) {
	restoreGlobals(t)

	runScraper = func(scrapexport.Config, io.Writer) (scrapexport.Report, error) {
		return scrapexport.Report{}, errors.New("boom")
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI([]string{"-url", "https://example.com", "-json", "a.json", "-csv", "a.csv"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "scrapexport error: boom") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMainUsesInjectedExit(t *testing.T) {
	restoreGlobals(t)

	cliArgs = []string{"scrapexport", "-url", "https://example.com", "-json", "out.json", "-csv", "out.csv"}
	runScraper = func(scrapexport.Config, io.Writer) (scrapexport.Report, error) {
		return scrapexport.Report{}, nil
	}

	gotCode := -1
	exitFunc = func(code int) {
		gotCode = code
	}

	main()

	if gotCode != 0 {
		t.Fatalf("exit code = %d, want 0", gotCode)
	}
}

func TestMultiStringFlagMethods(t *testing.T) {
	var m multiStringFlag
	if got := m.String(); got != "[]" {
		t.Fatalf("String() = %q", got)
	}
	if err := m.Set("x"); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if err := m.Set("y"); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	if got := m.String(); got != "[x y]" {
		t.Fatalf("String() = %q", got)
	}
}

func restoreGlobals(t *testing.T) {
	t.Helper()

	oldArgs := cliArgs
	oldStdout := stdoutW
	oldStderr := stderrW
	oldExit := exitFunc
	oldRun := runScraper

	t.Cleanup(func() {
		cliArgs = oldArgs
		stdoutW = oldStdout
		stderrW = oldStderr
		exitFunc = oldExit
		runScraper = oldRun
	})
}
