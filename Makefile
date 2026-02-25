.PHONY: run test cover build build-all backupsum-build scrapexport-build fmt shell

run:
	docker compose up --build app

test:
	docker compose run --rm app go test ./...

cover:
	docker compose run --rm app go test ./... -covermode=count -coverprofile=coverage.out
	docker compose run --rm app go tool cover -func=coverage.out

build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/app ./cmd/app

build-all:
	docker compose run --rm app sh -c 'go build -buildvcs=false ./cmd/...'

backupsum-build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/backupsum ./cmd/backupsum

scrapexport-build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/scrapexport ./cmd/scrapexport

fmt:
	docker compose run --rm app gofmt -w .

shell:
	docker compose run --rm app sh
