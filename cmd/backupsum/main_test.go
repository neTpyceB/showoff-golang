package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"showoff-golang/internal/backupsum"
)

func TestRunCLISuccess(t *testing.T) {
	restoreGlobals(t)

	var gotCfg backupsum.Config
	runTool = func(cfg backupsum.Config, stdout io.Writer) (backupsum.Report, error) {
		gotCfg = cfg
		_, _ = stdout.Write([]byte("{\"ok\":true}\n"))
		return backupsum.Report{}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI([]string{"-src", "from", "-dst", "to", "-report", "report.json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr should be empty, got %q", stderr.String())
	}
	if stdout.String() != "{\"ok\":true}\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if gotCfg.SourceDir != "from" || gotCfg.DestinationDir != "to" || gotCfg.ReportPath != "report.json" {
		t.Fatalf("unexpected cfg: %+v", gotCfg)
	}
}

func TestRunCLIParseError(t *testing.T) {
	restoreGlobals(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI([]string{"-unknown"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout should be empty, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunCLIServiceError(t *testing.T) {
	restoreGlobals(t)

	runTool = func(backupsum.Config, io.Writer) (backupsum.Report, error) { // patch import
		return backupsum.Report{}, errors.New("boom")
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := runCLI([]string{"-src", "x", "-dst", "y"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "backupsum error: boom") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMainUsesInjectedExit(t *testing.T) {
	restoreGlobals(t)

	cliArgs = []string{"backupsum", "-src", "x", "-dst", "y"}
	runTool = func(backupsum.Config, io.Writer) (backupsum.Report, error) {
		return backupsum.Report{}, nil
	}

	var exitCode int
	exitFunc = func(code int) {
		exitCode = code
	}

	main()

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
}

func restoreGlobals(t *testing.T) {
	t.Helper()

	oldArgs := cliArgs
	oldStdout := stdoutW
	oldStderr := stderrW
	oldExit := exitFunc
	oldRunTool := runTool

	t.Cleanup(func() {
		cliArgs = oldArgs
		stdoutW = oldStdout
		stderrW = oldStderr
		exitFunc = oldExit
		runTool = oldRunTool
	})
}
