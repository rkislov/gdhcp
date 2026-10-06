# GoDHCP

Высокопроизводительный DHCPv4-сервер: YAML-конфигурация, REST API, встроенный веб-интерфейс, VLAN и DHCP relay (опция 82).

Автор: Кислов Роман Сергеевич.

## Быстрый старт

Нужен Go 1.26+. Веб-интерфейс — обычные Go-шаблоны и JS, уже вшиты в бинарник (`go:embed`).

```bash
make build
sudo ./bin/godhcp -config configs/dev.yml
```

- API: <http://127.0.0.1:8080>
- Интерфейс: <http://127.0.0.1:8080/ui/>
- Swagger: <http://127.0.0.1:8080/swagger>
- Метрики: <http://127.0.0.1:9090>
- В `configs/dev.yml` пользователь `admin`, пароль `changeme`.

### Предупреждение: порт DHCP 67

По умолчанию сервер слушает **стандартный UDP-порт DHCP `:67`** (`server.listen`). Это привилегированный порт.

- На Linux процесс нужно запускать от **root** либо выдать capability: `CAP_NET_BIND_SERVICE`, `CAP_NET_RAW`, `CAP_NET_ADMIN` (для сырых сокетов, ping-check и netlink VLAN).
- В Docker: `--network host --cap-add=NET_ADMIN --cap-add=NET_RAW` (образ уже идёт от root).
- Без нужных прав bind на `:67` завершится ошибкой; в журнале будет предупреждение о привилегированном порте.
- Менять `server.listen` на нестандартный порт (например `:6767`) имеет смысл только на стенде без root — реальные клиенты DHCP ожидают порт **67**.

Проверка конфигурации: `./bin/godhcp -validate -config configs/example.yml`.

Перечитать файл без остановки процесса: `SIGHUP` или `POST /api/v1/config/reload`.

## Docker

```bash
docker build -f deploy/Dockerfile -t godhcp:1.0 .
docker run -d --name godhcp \
  --network host \
  --cap-add=NET_ADMIN --cap-add=NET_RAW \
  -v /etc/godhcp:/etc/godhcp \
  -v /var/lib/godhcp:/var/lib/godhcp \
  godhcp:1.0
```

Образ distroless запускается от root: порт 67 и `CAP_NET_RAW` иначе недоступны.

## Что входит в эту версию

- DHCPv4: DISCOVER, OFFER, REQUEST, ACK, NAK, DECLINE, RELEASE, INFORM.
- Пулы, резервации по MAC и client-id, authoritative / non-authoritative, ping-check, PXE (`next_server`, `boot_file`).
- VLAN: сопоставление интерфейса и идентификатора с подсетью, PCP в кадре Ethernet, опция 43, неизвестный VLAN (`ignore` / `nak` / `default-pool`). Создание VLAN-интерфейса через netlink — на Linux, если `server.manage_links: true`.
- Relay: опция 82 (sub-option 1, 2, 5, 151, 152), эхо без изменений, link-selection, доверенные relay, лимит hops, парсеры Circuit ID (Cisco, Huawei, MikroTik, Juniper, Aruba, HP, Extreme, Brocade, UniFi и свои regex).
- SQLite по умолчанию и PostgreSQL. Выдача адреса транзакционная.
- REST API, JWT и API-ключи, роли `admin` / `operator` / `viewer`.
- Веб-интерфейс на Go `html/template` + JS: обзор, аренды, подсети, резервации, VLAN, relay, редактор YAML, журнал (SSE), пользователи.
- Метрики Prometheus, JSON-логи (`log/slog`), `/healthz` и `/readyz`.

DHCPv6, обновления DDNS и отказоустойчивость по RFC 8156 в эту поставку не входят: в конфигурации есть заготовки, рабочий протокол — DHCPv4. Балансировка `ha` делит новых клиентов по хэшу RFC 3074 и не совместима с ISC failover по проводу.

## Документация

- [Архитектура](docs/architecture.md)
- [Конфигурация](docs/configuration.md)
- [API](docs/api.md)
- [VLAN](docs/vlans.md)
- [Relay](docs/relay.md)
- [Типовые проблемы](docs/troubleshooting.md)
- [Лицензия и зависимости](docs/license.md)
- man-страница: `docs/godhcp.8`

Примеры: `configs/example.yml`, `configs/office.yml`, `configs/isp.yml`, `configs/voip.yml`, `configs/guest-wifi.yml`, `configs/pxe.yml`.

## Тесты

```bash
go test ./...
```

## Лицензия

Apache License, Version 2.0. Copyright 2026 Кислов Роман Сергеевич.

Полный текст — в файле [LICENSE](LICENSE). Уведомление об авторстве — в [NOTICE](NOTICE). Сторонние компоненты — в [THIRD_PARTY_LICENSES](THIRD_PARTY_LICENSES).

Программа распространяется «AS IS», без гарантий, как описано в разделах 7 и 8 Apache License 2.0.
