# `backupsum` CLI (backup + checksum)

Recursive file backup tool with SHA-256 checksums and JSON report output.

## Binary

- Source: `/Users/vadimsduboiss/Codebase/showoff-golang/cmd/backupsum`
- Build output (example): `/Users/vadimsduboiss/Codebase/showoff-golang/bin/backupsum`

## Features

- Recursively reads a source directory
- Copies regular files to destination directory
- Preserves file permissions for copied files
- Computes SHA-256 checksum while copying (single pass)
- Prints JSON report to stdout
- Optionally writes the same JSON report to a file
- Skips non-regular files (for example symlinks)

## Usage

```bash
docker compose run --rm app go run ./cmd/backupsum -src <source_dir> -dst <destination_dir> [-report <json_file>]
```

## Example (inside repo workspace)

```bash
mkdir -p tmp/demo-src/nested
printf 'alpha' > tmp/demo-src/a.txt
printf 'beta' > tmp/demo-src/nested/b.txt

docker compose run --rm app go run ./cmd/backupsum \
  -src ./tmp/demo-src \
  -dst ./tmp/demo-backup \
  -report ./tmp/demo-report.json
```

Example output (stdout JSON):

```json
{
  "source_dir": "/workspace/tmp/demo-src",
  "destination_dir": "/workspace/tmp/demo-backup",
  "files": [
    {
      "relative_path": "a.txt",
      "size_bytes": 5,
      "sha256": "8ed3f6ad685b959ead7022518e1af76cd816f8e8ec7ccdda1ed4018e8f2223f8"
    },
    {
      "relative_path": "nested/b.txt",
      "size_bytes": 4,
      "sha256": "f44e64e75f3948e9f73f8dfa94721c4ce8cbb4f265c4790c702b2d41cfbf2753"
    }
  ],
  "summary": {
    "file_count": 2,
    "total_bytes": 9
  }
}
```

## Exit Codes

- `0` success
- `1` runtime error (filesystem/copy/report write)
- `2` CLI argument parsing error

## Build Commands

```bash
make backupsum-build
docker compose run --rm app go build -buildvcs=false -o ./bin/backupsum ./cmd/backupsum
```
