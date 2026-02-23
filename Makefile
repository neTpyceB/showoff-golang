.PHONY: run test cover build fmt shell

run:
	docker compose up --build app

test:
	docker compose run --rm app go test ./...

cover:
	docker compose run --rm app go test ./... -covermode=count -coverprofile=coverage.out
	docker compose run --rm app go tool cover -func=coverage.out

build:
	docker compose run --rm app go build -buildvcs=false -o ./bin/app ./cmd/app

fmt:
	docker compose run --rm app gofmt -w .

shell:
	docker compose run --rm app sh
