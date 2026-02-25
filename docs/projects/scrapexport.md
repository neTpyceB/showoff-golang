# `scrapexport` CLI (web scraper + parser + exporter)

Fetches web pages, extracts selected content, and exports both JSON and CSV.

## Binary

- Source: `/Users/vadimsduboiss/Codebase/showoff-golang/cmd/scrapexport`
- Build output (example): `/Users/vadimsduboiss/Codebase/showoff-golang/bin/scrapexport`

## Extracted Fields

For each URL:

- `url`
- `status_code`
- `title` (first `<title>`)
- `h1` (first `<h1>`)
- `meta_description` (first `<meta name="description">` or `<meta property="og:description">`)

## Usage

```bash
docker compose run --rm app go run ./cmd/scrapexport \
  -url https://example.com \
  -url https://example.org \
  -json ./tmp/scrape-report.json \
  -csv ./tmp/scrape-report.csv \
  -timeout 10s
```

## Outputs

- Prints JSON report to stdout
- Writes JSON report file (`-json`)
- Writes CSV file (`-csv`)

## Example CSV Header

```csv
url,status_code,title,h1,meta_description
```

## Exit Codes

- `0` success
- `1` runtime error (fetch/parse/export)
- `2` CLI argument parsing error

## Build

```bash
make scrapexport-build
docker compose run --rm app go build -buildvcs=false -o ./bin/scrapexport ./cmd/scrapexport
```

## Notes

- Requires network access from the container to reach target URLs.
- Non-200 responses are still parsed and exported (status code is recorded).
