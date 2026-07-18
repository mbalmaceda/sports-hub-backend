.PHONY: run build tidy test migrate-up migrate-down

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

tidy:
	go mod tidy

test:
	go test ./... -race

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down
