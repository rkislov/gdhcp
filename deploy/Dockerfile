# Copyright 2026 Кислов Роман Сергеевич
# Licensed under the Apache License, Version 2.0.

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/godhcp ./cmd/godhcp

FROM gcr.io/distroless/static-debian12
# DHCP port 67 and NET_RAW/NET_ADMIN require root inside the container.
USER 0
LABEL org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.authors="Кислов Роман Сергеевич"
COPY --from=build /out/godhcp /godhcp
COPY LICENSE NOTICE THIRD_PARTY_LICENSES /usr/share/licenses/godhcp/
COPY third_party/ /usr/share/licenses/godhcp/third_party/
EXPOSE 67/udp 8181/tcp 9090/tcp
ENTRYPOINT ["/godhcp"]
CMD ["-config", "/etc/godhcp/config.yml"]
