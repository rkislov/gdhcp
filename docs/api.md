# API

© 2026 Кислов Роман Сергеевич. Apache License 2.0.

Спецификация OpenAPI 3.1: `api/openapi.yaml`, в работающем процессе — `GET /api/openapi.yaml`. Swagger UI: `/swagger`.

Аутентификация: `Authorization: Bearer <jwt>`, заголовок `X-API-Key` или query `access_token` (его использует живой журнал, потому что EventSource не ставит заголовок).

Если `api.auth.users` и `api.auth.api_keys` пусты, защищённые маршруты возвращают 401; автоматического администратора `anonymous` нет. Для входа через веб создайте пользователя в `api.auth.users`, задайте `jwt_secret` и хеш пароля из `godhcp hash-password -password 'ВАШ_ПАРОЛЬ'`. После изменения настроек аутентификации перезапустите службу и откройте `/ui/login`. Сохранённая действующая сессия позволяет входить без повторного ввода пароля; для проверки нового входа используйте приватное окно браузера.

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


## Доступ и парсеры в 0.1.9

Роли и область `vlan_ids` проверяются сервером для отдельных объектов и списков. Глобальные настройки требуют глобальной роли; VLAN-администратор получает 403 для `/config`, `/users`, `/access`, общего журнала и метрик. Подробнее: [модель доступа](access-control.md).

`GET /access` возвращает скрытые настройки LDAP, группы и VLAN; `PUT /access/ldap` сохраняет LDAP; `POST /access/ldap/test` проверяет поиск пользователя (`{ldap: {...}, username: "..."}`) без сохранения; `POST /access/groups` создаёт или заменяет группу; `DELETE /access/groups/{name}` удаляет неиспользуемую группу. `PUT /users/{username}` меняет роль, `vlan_ids`, `groups` и опционально пароль. Пустой пароль при PUT сохраняет прежний.

`GET /parsers/circuit-id` возвращает эффективный `items` и пользовательский `custom`; POST создаёт или заменяет парсер по имени. `GET /parsers/circuit-id/profiles` возвращает пресеты производителей. DELETE с именем удаляет пользовательский шаблон. POST `/parsers/circuit-id/test` принимает имя парсера и Circuit ID либо `regex` для проверки несохранённого шаблона. Явно выбранный парсер проверяется без fallback.
