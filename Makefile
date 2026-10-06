.PHONY: build test cover docker tidy release

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/godhcp ./cmd/godhcp

test:
	go test ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

tidy:
	go mod tidy

docker:
	docker build -f deploy/Dockerfile -t godhcp:1.0 .

# Cross-compiled binaries for common platforms (Raspberry Pi: armv7 + arm64).
release:
	mkdir -p dist
	GOOS=linux   GOARCH=amd64            CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/godhcp-linux-amd64 ./cmd/godhcp
	GOOS=linux   GOARCH=386              CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/godhcp-linux-386 ./cmd/godhcp
	GOOS=linux   GOARCH=arm   GOARM=7    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/godhcp-linux-armv7 ./cmd/godhcp
	GOOS=linux   GOARCH=arm64            CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/godhcp-linux-arm64 ./cmd/godhcp
	GOOS=windows GOARCH=amd64            CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/godhcp-windows-amd64.exe ./cmd/godhcp
	GOOS=windows GOARCH=386              CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/godhcp-windows-386.exe ./cmd/godhcp
	ls -lh dist
