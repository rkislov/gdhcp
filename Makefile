.PHONY: build test cover web docker tidy

build: web
	go build -o bin/godhcp ./cmd/godhcp

web:
	cd web && npm ci && npm run build

test:
	go test ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

tidy:
	go mod tidy

docker:
	docker build -f deploy/Dockerfile -t godhcp:1.0 .
