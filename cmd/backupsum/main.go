package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"showoff-golang/internal/backupsum"
)

var (
	cliArgs            = os.Args
	stdoutW  io.Writer = os.Stdout
	stderrW  io.Writer = os.Stderr
	exitFunc           = os.Exit
	runTool            = backupsum.Run
)

func main() {
	exitFunc(runCLI(cliArgs[1:], stdoutW, stderrW))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backupsum", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var cfg backupsum.Config
	fs.StringVar(&cfg.SourceDir, "src", "", "source directory")
	fs.StringVar(&cfg.DestinationDir, "dst", "", "destination directory")
	fs.StringVar(&cfg.ReportPath, "report", "", "optional JSON report file path")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Usage: backupsum -src <dir> -dst <dir> [-report <file>]\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if _, err := runTool(cfg, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "backupsum error: %v\n", err)
		return 1
	}

	return 0
}
