APP_NAME=catalog-enricher

.PHONY: run test build docker-up docker-down

run:
	go run ./cmd/api

test:
	go test -race -v -cover ./...

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/$(APP_NAME) ./cmd/api

docker-up:
	docker-compose up --build

docker-down:
	docker-compose down