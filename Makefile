# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0

.PHONY: build test cover docker tidy release packages admin-guide-pdf web-guide-pdf binaries test-dhcp-docker test-systemd-vm test-load-docker
.DEFAULT_GOAL := build

test-load-docker:
	bash scripts/test-load-docker.sh

test-systemd-vm:
	bash scripts/test-systemd-vm.sh

test-dhcp-docker:
	bash scripts/test-dhcp-docker.sh

packages:
	bash scripts/build-packages.sh

admin-guide-pdf:
	python3 scripts/build-admin-pdf.py
	cp output/pdf/godhcp-admin-guide.pdf docs/godhcp-admin-guide.pdf

web-guide-pdf:
	python3 scripts/build-web-guide-pdf.py
	cp output/pdf/godhcp-web-guide.pdf docs/godhcp-web-guide.pdf

binaries:
	bash scripts/build-binaries.sh

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/godhcp ./cmd/godhcp

test:
	go test ./...

cover:
	GOTOOLCHAIN=go1.26.0 go test -coverprofile=coverage.out ./...
	GOTOOLCHAIN=go1.26.0 go tool cover -func=coverage.out
	@awk 'NR > 1 { total += $$2; if ($$3 > 0) covered += $$2 } END { pct = total ? 100 * covered / total : 0; printf "Coverage: %.2f%% (required: 70%%)\n", pct; exit (pct < 70) }' coverage.out

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
