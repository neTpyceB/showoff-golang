.PHONY: run run-pipeline test cover build build-all backupsum-build scrapexport-build publisher-build consumer-build fmt shell

run:
	docker compose up --build app

run-pipeline:
	docker compose up --build app outbox-publisher event-consumer

test:
	docker compose run --rm app go test ./... -count=1

cover:
	docker compose run --rm app go test ./... -count=1 -covermode=count -coverprofile=coverage.out
	docker compose run --rm app go tool cover -func=coverage.out

build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/app ./cmd/app

build-all:
	docker compose run --rm app sh -c 'go build -buildvcs=false ./cmd/...'

backupsum-build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/backupsum ./cmd/backupsum

scrapexport-build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/scrapexport ./cmd/scrapexport

publisher-build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/outboxpublisher ./cmd/outboxpublisher

consumer-build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/eventconsumer ./cmd/eventconsumer

fmt:
	docker compose run --rm app gofmt -w .

shell:
	docker compose run --rm app sh
