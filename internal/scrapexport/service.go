package scrapexport

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type Config struct {
	URLs     []string
	JSONPath string
	CSVPath  string
	Timeout  time.Duration
}

type Report struct {
	Pages   []PageRecord `json:"pages"`
	Summary Summary      `json:"summary"`
}

type PageRecord struct {
	URL             string `json:"url"`
	StatusCode      int    `json:"status_code"`
	Title           string `json:"title"`
	H1              string `json:"h1"`
	MetaDescription string `json:"meta_description"`
}

type Summary struct {
	PageCount int `json:"page_count"`
}

var (
	jsonMarshalIndentFn = json.MarshalIndent
	writeFileFn         = os.WriteFile
	createFileFn        = os.Create
	newRequestFn        = http.NewRequestWithContext
	htmlParseFn         = html.Parse
)

func Run(cfg Config, stdout io.Writer) (Report, error) {
	cfg = normalizeConfig(cfg)
	if err := validateConfig(cfg); err != nil {
		return Report{}, err
	}

	client := &http.Client{Timeout: cfg.Timeout}
	report := Report{Pages: make([]PageRecord, 0, len(cfg.URLs))}

	for _, rawURL := range cfg.URLs {
		record, err := fetchAndParse(client, rawURL)
		if err != nil {
			return Report{}, err
		}
		report.Pages = append(report.Pages, record)
	}
	report.Summary.PageCount = len(report.Pages)

	data, err := jsonMarshalIndentFn(report, "", "  ")
	if err != nil {
		return Report{}, fmt.Errorf("marshal json report: %w", err)
	}
	data = append(data, '\n')

	if _, err := stdout.Write(data); err != nil {
		return Report{}, fmt.Errorf("write stdout report: %w", err)
	}
	if err := writeFileFn(cfg.JSONPath, data, 0o644); err != nil {
		return Report{}, fmt.Errorf("write json file: %w", err)
	}
	if err := writeCSV(cfg.CSVPath, report.Pages); err != nil {
		return Report{}, err
	}

	return report, nil
}

func normalizeConfig(cfg Config) Config {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	return cfg
}

func validateConfig(cfg Config) error {
	if len(cfg.URLs) == 0 {
		return errors.New("at least one -url is required")
	}
	if cfg.JSONPath == "" {
		return errors.New("-json output path is required")
	}
	if cfg.CSVPath == "" {
		return errors.New("-csv output path is required")
	}
	for _, u := range cfg.URLs {
		if strings.TrimSpace(u) == "" {
			return errors.New("url entries must not be empty")
		}
	}
	return nil
}

func fetchAndParse(client *http.Client, rawURL string) (PageRecord, error) {
	req, err := newRequestFn(context.Background(), http.MethodGet, rawURL, nil)
	if err != nil {
		return PageRecord{}, fmt.Errorf("build request for %q: %w", rawURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return PageRecord{}, fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()

	parsed, err := parseHTMLDocument(resp.Body)
	if err != nil {
		return PageRecord{}, fmt.Errorf("parse %q: %w", rawURL, err)
	}

	return PageRecord{
		URL:             rawURL,
		StatusCode:      resp.StatusCode,
		Title:           parsed.Title,
		H1:              parsed.H1,
		MetaDescription: parsed.MetaDescription,
	}, nil
}

type parsedDocument struct {
	Title           string
	H1              string
	MetaDescription string
}

func parseHTMLDocument(r io.Reader) (parsedDocument, error) {
	doc, err := htmlParseFn(r)
	if err != nil {
		return parsedDocument{}, fmt.Errorf("parse html: %w", err)
	}

	var out parsedDocument
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}

		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				if out.Title == "" {
					out.Title = nodeText(n)
				}
			case "h1":
				if out.H1 == "" {
					out.H1 = nodeText(n)
				}
			case "meta":
				if out.MetaDescription == "" && isMetaDescription(n) {
					out.MetaDescription = strings.TrimSpace(attrValue(n, "content"))
				}
			}
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	return out, nil
}

func writeCSV(path string, pages []PageRecord) error {
	file, err := createFileFn(path)
	if err != nil {
		return fmt.Errorf("create csv file: %w", err)
	}
	defer file.Close()

	w := csv.NewWriter(file)
	rows := [][]string{{"url", "status_code", "title", "h1", "meta_description"}}
	for _, p := range pages {
		rows = append(rows, []string{
			p.URL,
			fmt.Sprintf("%d", p.StatusCode),
			p.Title,
			p.H1,
			p.MetaDescription,
		})
	}
	if err := w.WriteAll(rows); err != nil {
		return fmt.Errorf("write csv rows: %w", err)
	}
	return nil
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil {
			return
		}
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
			b.WriteByte(' ')
		}
		for child := cur.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func isMetaDescription(n *html.Node) bool {
	name := strings.ToLower(attrValue(n, "name"))
	if name == "description" {
		return true
	}
	property := strings.ToLower(attrValue(n, "property"))
	return property == "og:description"
}

func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
