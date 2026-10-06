# Copyright 2026 Кислов Роман Сергеевич
# Licensed under the Apache License, Version 2.0.

FROM node:22-bookworm AS web
WORKDIR /src
COPY web/package.json web/package-lock.json web/
COPY web web
WORKDIR /src/web
RUN npm ci && npm run build

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/godhcp ./cmd/godhcp

FROM gcr.io/distroless/static-debian12
# DHCP port 67 and NET_RAW/NET_ADMIN require root inside the container.
USER 0
COPY --from=build /out/godhcp /godhcp
EXPOSE 67/udp 8080/tcp 9090/tcp
ENTRYPOINT ["/godhcp"]
CMD ["-config", "/etc/godhcp/config.yml"]
