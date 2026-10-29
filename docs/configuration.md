# Конфигурация

© 2026 Кислов Роман Сергеевич. Apache License 2.0.

Файл YAML. Подстановки: `${VAR}`, `${VAR:-значение}`, литерал `$$`.

| Секция | Назначение |
|--------|------------|
| `server` | Интерфейсы, authoritative, ping-check, сроки аренды, `listen` (по умолчанию `:67`), `server_id` |
| `database` | `sqlite` или `postgres` и DSN |
| `api` | Адрес HTTP, TLS, JWT, пользователи, API-ключи, `rate_limit` |
| `web` | `enabled`, `path` (по умолчанию `/ui`) |
| `vlans` | Идентификатор, имя, интерфейс, PCP `priority` 0–7, `subnet_ref`, опции |
| `subnets` | Сеть, диапазон, шлюз, DNS, домен, VLAN, резервации, PXE |
| `reservations` | Резервации вне подсети: нужен `subnet_id` |
| `relay` | Доверенные агенты, парсеры Circuit ID, hops, действие для неизвестного VLAN |
| `classes` | OUI или vendor-class → подсеть |
| `logging` | `debug`/`info`/`warn`/`error`, `json` или `text`, файл |
| `metrics` | Prometheus listen |
| `ha` | Доля хэша клиента: `primary` отвечает при `hash < split` |
| `dhcpv6`, `ddns` | Заготовки следующих этапов, протокол не обслуживается |

`relay.unknown_vlan_action`: `ignore`, `nak`, `default-pool`. Для последнего укажите `default_pool`.

`relay.vlan_source_priority` принимает только:

- `option82_sub5_link_selection`
- `option82_sub1_circuit_id`
- `giaddr`
- `default_vlan`

Пароль пользователя хранится как argon2id:

```bash
godhcp hash-password -password 'secret'
```

Если список пользователей пуст и API-ключей нет, REST открыт с ролью admin. Так удобно только на стенде.

`server.manage_links: true` на Linux создаёт VLAN-интерфейс при `POST /api/v1/vlans`. На других ОС вызов вернёт предупреждение, запись в конфиге сохранится.

## Порт DHCP и привилегии

`server.listen` по умолчанию — **`:67`** (стандартный порт DHCPv4-сервера).

| Среда | Требование |
|-------|------------|
| Linux | root или `CAP_NET_BIND_SERVICE` + `CAP_NET_RAW` + `CAP_NET_ADMIN` |
| Docker | `--network host --cap-add=NET_ADMIN --cap-add=NET_RAW` |
| macOS / Windows (стенд) | запуск с правами администратора; для локальной отладки без привилегий можно временно указать другой порт, например `:6767` |

Реальные DHCP-клиенты и relay обращаются на порт **67**. Нестандартный `server.listen` — только для разработки.

Полный образец — `configs/example.yml`.

### Страничный кеш SQLite

Стандартный DSN использует `cache_size=-32768` (до 32 MiB страниц на соединение, с выделением по потребности). На дисковой БД работают соединение записи и отдельное соединение checkpoint; учитывайте память обоих. Параметр повторно применяется при замене соединения `database/sql`. Собственные `_pragma` в URI DSN сохраняют пользовательскую политику. Увеличение кеша не гарантирует верхнюю границу задержки commit: см. [проверку хвоста задержек](performance.md#проверка-увеличенного-кеша--26-октября-2026).
