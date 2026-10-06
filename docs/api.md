# API

Спецификация OpenAPI 3.1: `api/openapi.yaml`, в работающем процессе — `GET /api/openapi.yaml`. Swagger UI: `/swagger`.

Аутентификация: `Authorization: Bearer <jwt>`, заголовок `X-API-Key` или query `access_token` (его использует живой журнал, потому что EventSource не ставит заголовок).

Токены: `POST /api/v1/auth/login` с `{"username","password"}` возвращает `access_token`, `refresh_token`, `role`. Обновление: `POST /api/v1/auth/refresh` с `refresh_token`.

Роли по возрастанию: `viewer`, `operator`, `admin`.

| Метод | Путь | Кто |
|-------|------|-----|
| GET | `/healthz`, `/readyz` | без токена |
| GET | `/api/v1/status`, `/api/v1/metrics` | viewer |
| GET | `/api/v1/leases`, `/api/v1/leases/{ip}` | viewer |
| DELETE | `/api/v1/leases/{ip}` | operator |
| POST | `/api/v1/leases/{ip}/reserve` | operator |
| GET/POST | `/api/v1/subnets`, `PUT/DELETE /api/v1/subnets/{id}` | чтение viewer, запись admin |
| GET/POST/DELETE | `/api/v1/reservations` | запись operator |
| GET/POST/PUT/DELETE | `/api/v1/vlans` и `/leases`, `/stats` | запись admin |
| GET/POST/DELETE | `/api/v1/relays`, `/stats` | запись admin |
| GET/POST | `/api/v1/parsers/circuit-id` | запись admin |
| POST | `/api/v1/parsers/circuit-id/test` | operator |
| GET/PUT | `/api/v1/config` | admin |
| POST | `/api/v1/config/validate` | admin |
| POST | `/api/v1/config/reload` | operator |
| GET | `/api/v1/logs/stream` | viewer, `text/event-stream` |
| GET/POST/DELETE | `/api/v1/users` | admin |

Фильтры аренд: `q`, `vlan`, `subnet`, `giaddr`, `state`, `limit`, `offset`.

Ошибки — JSON `{"error":{"code","message"}}` и код HTTP по смыслу RFC 7231.

`GET /api/v1/config` без `Accept: application/json` отдаёт YAML. Секрет JWT и API-ключи заменяются на `***`. Такой документ нельзя записать обратно, пока секреты не восстановлены.

Журнал — Server-Sent Events, не WebSocket: одна зависимость меньше, браузерный `EventSource` читает поток напрямую.
