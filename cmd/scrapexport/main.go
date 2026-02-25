package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"showoff-golang/internal/scrapexport"
)

var (
	cliArgs              = os.Args
	stdoutW    io.Writer = os.Stdout
	stderrW    io.Writer = os.Stderr
	exitFunc             = os.Exit
	runScraper           = scrapexport.Run
)

func main() {
	exitFunc(runCLI(cliArgs[1:], stdoutW, stderrW))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrapexport", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var urls multiStringFlag
	var cfg scrapexport.Config

	fs.Var(&urls, "url", "page URL to fetch (repeat flag for multiple pages)")
	fs.StringVar(&cfg.JSONPath, "json", "", "output JSON file path")
	fs.StringVar(&cfg.CSVPath, "csv", "", "output CSV file path")
	fs.DurationVar(&cfg.Timeout, "timeout", 10*time.Second, "HTTP timeout (e.g. 5s, 2m)")

	fs.Usage = func() {
		_, _ = fmt.Fprintf(stderr, "Usage: scrapexport -url <url> [-url <url> ...] -json <file> -csv <file> [-timeout 10s]\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg.URLs = urls
	if _, err := runScraper(cfg, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "scrapexport error: %v\n", err)
		return 1
	}

	return 0
}

type multiStringFlag []string

func (m *multiStringFlag) String() string {
	return fmt.Sprintf("%v", []string(*m))
}

func (m *multiStringFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}
