package scrapexport

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/html"
)

func TestRunFetchesParsesAndExportsJSONAndCSV(t *testing.T) {
	restorePackageFns(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Page A</title><meta name="description" content="Alpha page"></head><body><h1>Alpha</h1></body></html>`)
		case "/b":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>Page B</title><meta property="og:description" content="Beta page"></head><body><h1>Beta <span>Title</span></h1></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	jsonPath := filepath.Join(tmp, "report.json")
	csvPath := filepath.Join(tmp, "report.csv")

	var stdout bytes.Buffer
	report, err := Run(Config{
		URLs:     []string{srv.URL + "/a", srv.URL + "/b"},
		JSONPath: jsonPath,
		CSVPath:  csvPath,
		Timeout:  2 * time.Second,
	}, &stdout)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if report.Summary.PageCount != 2 {
		t.Fatalf("page_count = %d, want 2", report.Summary.PageCount)
	}
	if report.Pages[0].Title != "Page A" || report.Pages[0].H1 != "Alpha" || report.Pages[0].MetaDescription != "Alpha page" {
		t.Fatalf("page[0] = %+v", report.Pages[0])
	}
	if report.Pages[1].StatusCode != http.StatusNotFound {
		t.Fatalf("status code = %d", report.Pages[1].StatusCode)
	}
	if report.Pages[1].H1 != "Beta Title" {
		t.Fatalf("h1 = %q", report.Pages[1].H1)
	}
	if report.Pages[1].MetaDescription != "Beta page" {
		t.Fatalf("meta = %q", report.Pages[1].MetaDescription)
	}

	stdoutReport := decodeReport(t, stdout.Bytes())
	fileReport := decodeReport(t, mustReadFile(t, jsonPath))
	if stdoutReport.Summary != report.Summary || fileReport.Summary != report.Summary {
		t.Fatalf("summary mismatch")
	}

	rows := readCSVRows(t, csvPath)
	if len(rows) != 3 {
		t.Fatalf("csv rows = %d, want 3", len(rows))
	}
	if got := rows[0]; strings.Join(got, ",") != "url,status_code,title,h1,meta_description" {
		t.Fatalf("csv header = %#v", got)
	}
}

func TestRunValidationAndOutputErrors(t *testing.T) {
	restorePackageFns(t)

	t.Run("missing urls", func(t *testing.T) {
		_, err := Run(Config{JSONPath: "a.json", CSVPath: "a.csv"}, &bytes.Buffer{})
		assertErrorContains(t, err, "at least one -url")
	})

	t.Run("missing json", func(t *testing.T) {
		_, err := Run(Config{URLs: []string{"https://example.com"}, CSVPath: "a.csv"}, &bytes.Buffer{})
		assertErrorContains(t, err, "-json output path")
	})

	t.Run("missing csv", func(t *testing.T) {
		_, err := Run(Config{URLs: []string{"https://example.com"}, JSONPath: "a.json"}, &bytes.Buffer{})
		assertErrorContains(t, err, "-csv output path")
	})

	t.Run("empty url entry", func(t *testing.T) {
		_, err := Run(Config{URLs: []string{" "}, JSONPath: "a.json", CSVPath: "a.csv"}, &bytes.Buffer{})
		assertErrorContains(t, err, "must not be empty")
	})

	t.Run("stdout write error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html><head><title>X</title></head><body><h1>Y</h1></body></html>`)
		}))
		defer srv.Close()

		_, err := Run(Config{
			URLs:     []string{srv.URL},
			JSONPath: filepath.Join(t.TempDir(), "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "a.csv"),
		}, failWriter{})
		assertErrorContains(t, err, "write stdout report")
	})

	t.Run("json file write error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html><head><title>X</title></head><body><h1>Y</h1></body></html>`)
		}))
		defer srv.Close()

		_, err := Run(Config{
			URLs:     []string{srv.URL},
			JSONPath: filepath.Join(t.TempDir(), "missing", "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "a.csv"),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "write json file")
	})

	t.Run("csv file create error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html><head><title>X</title></head><body><h1>Y</h1></body></html>`)
		}))
		defer srv.Close()

		_, err := Run(Config{
			URLs:     []string{srv.URL},
			JSONPath: filepath.Join(t.TempDir(), "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "missing", "a.csv"),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "create csv file")
	})
}

func TestRunFetchAndParseErrors(t *testing.T) {
	restorePackageFns(t)

	t.Run("request build error", func(t *testing.T) {
		_, err := Run(Config{
			URLs:     []string{":// bad-url"},
			JSONPath: filepath.Join(t.TempDir(), "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "a.csv"),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "build request")
	})

	t.Run("fetch error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		srv.Close()

		_, err := Run(Config{
			URLs:     []string{srv.URL},
			JSONPath: filepath.Join(t.TempDir(), "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "a.csv"),
			Timeout:  time.Second,
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "fetch")
	})

	t.Run("parse error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<html></html>")
		}))
		defer srv.Close()

		htmlParseFn = func(io.Reader) (*html.Node, error) {
			return nil, errors.New("parse failed")
		}

		_, err := Run(Config{
			URLs:     []string{srv.URL},
			JSONPath: filepath.Join(t.TempDir(), "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "a.csv"),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "parse")
	})
}

func TestRunMarshalErrorAndCSVWriteError(t *testing.T) {
	t.Run("marshal error", func(t *testing.T) {
		restorePackageFns(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html><head><title>X</title></head><body><h1>Y</h1></body></html>`)
		}))
		defer srv.Close()

		jsonMarshalIndentFn = func(any, string, string) ([]byte, error) {
			return nil, errors.New("marshal failed")
		}

		_, err := Run(Config{
			URLs:     []string{srv.URL},
			JSONPath: filepath.Join(t.TempDir(), "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "a.csv"),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "marshal json report")
	})

	t.Run("csv write error", func(t *testing.T) {
		restorePackageFns(t)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `<html><head><title>X</title></head><body><h1>Y</h1></body></html>`)
		}))
		defer srv.Close()

		createFileFn = func(string) (*os.File, error) {
			f, err := os.CreateTemp(t.TempDir(), "csv-*")
			if err != nil {
				t.Fatalf("CreateTemp: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("close temp file: %v", err)
			}
			ro, err := os.Open(f.Name())
			if err != nil {
				t.Fatalf("open temp file readonly: %v", err)
			}
			return ro, nil
		}

		_, err := Run(Config{
			URLs:     []string{srv.URL},
			JSONPath: filepath.Join(t.TempDir(), "a.json"),
			CSVPath:  filepath.Join(t.TempDir(), "a.csv"),
		}, &bytes.Buffer{})
		assertErrorContains(t, err, "write csv rows")
	})
}

func TestNormalizeValidateAndParseHelpers(t *testing.T) {
	restorePackageFns(t)

	if got := normalizeConfig(Config{}).Timeout; got != 10*time.Second {
		t.Fatalf("default timeout = %v", got)
	}
	if got := normalizeConfig(Config{Timeout: 2 * time.Second}).Timeout; got != 2*time.Second {
		t.Fatalf("kept timeout = %v", got)
	}

	doc, err := parseHTMLDocument(strings.NewReader(`
		<html>
			<head>
				<title>  Hello   World </title>
				<meta name="description" content="desc">
			</head>
			<body>
				<h1>Hello <span>Go</span></h1>
			</body>
		</html>
	`))
	if err != nil {
		t.Fatalf("parseHTMLDocument error: %v", err)
	}
	if doc.Title != "Hello World" || doc.H1 != "Hello Go" || doc.MetaDescription != "desc" {
		t.Fatalf("parsed doc = %+v", doc)
	}

	doc, err = parseHTMLDocument(strings.NewReader(`<html><head></head><body></body></html>`))
	if err != nil {
		t.Fatalf("parseHTMLDocument error: %v", err)
	}
	if doc.Title != "" || doc.H1 != "" || doc.MetaDescription != "" {
		t.Fatalf("expected empty fields, got %+v", doc)
	}

	metaNode := &html.Node{
		Type: html.ElementNode,
		Data: "meta",
		Attr: []html.Attribute{{Key: "property", Val: "og:description"}},
	}
	if !isMetaDescription(metaNode) {
		t.Fatal("expected og:description meta to be detected")
	}
	if got := attrValue(metaNode, "missing"); got != "" {
		t.Fatalf("attrValue missing = %q", got)
	}
	if got := nodeText(&html.Node{Type: html.TextNode, Data: "plain"}); got != "plain" {
		t.Fatalf("nodeText text node = %q", got)
	}
	if got := nodeText(nil); got != "" {
		t.Fatalf("nodeText nil = %q", got)
	}
}

func TestParseHTMLDocumentDuplicateTagsAndNilDoc(t *testing.T) {
	restorePackageFns(t)

	doc, err := parseHTMLDocument(strings.NewReader(`
		<html>
			<head>
				<title>First</title>
				<title>Second</title>
				<meta name="description" content="first-meta">
				<meta property="og:description" content="second-meta">
			</head>
			<body>
				<h1>First H1</h1>
				<h1>Second H1</h1>
			</body>
		</html>
	`))
	if err != nil {
		t.Fatalf("parseHTMLDocument error: %v", err)
	}
	if doc.Title != "First" || doc.H1 != "First H1" || doc.MetaDescription != "first-meta" {
		t.Fatalf("unexpected first-value selection: %+v", doc)
	}

	htmlParseFn = func(io.Reader) (*html.Node, error) {
		return nil, nil
	}
	doc, err = parseHTMLDocument(strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseHTMLDocument nil doc error: %v", err)
	}
	if doc != (parsedDocument{}) {
		t.Fatalf("expected empty parsedDocument, got %+v", doc)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func decodeReport(t *testing.T, data []byte) Report {
	t.Helper()
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal report: %v\n%s", err, data)
	}
	return r
}

func readCSVRows(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open csv: %v", err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	return rows
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file %s: %v", path, err)
	}
	return data
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

	oldJSONMarshal := jsonMarshalIndentFn
	oldWriteFile := writeFileFn
	oldCreateFile := createFileFn
	oldNewReq := newRequestFn
	oldParse := htmlParseFn

	t.Cleanup(func() {
		jsonMarshalIndentFn = oldJSONMarshal
		writeFileFn = oldWriteFile
		createFileFn = oldCreateFile
		newRequestFn = oldNewReq
		htmlParseFn = oldParse
	})
}
