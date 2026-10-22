# Импорт из ISC dhcpd.conf

© 2026 Кислов Роман Сергеевич. Apache License 2.0.

Полная процедура: [экспорт исходных настроек ISC DHCP, проверка переноса и переключение](admin-guide.md#экспорт-настроек-из-isc-dhcp).

```bash
godhcp import-dhcpd -in /etc/dhcp/dhcpd.conf -out /etc/godhcp/config.yml
```

Без `-out` YAML печатается в stdout. Предупреждения и список прочитанных файлов — в stderr.

## Include

Директива `include "path";` (и `include 'path';`) раскрывается рекурсивно:

- относительный путь считается от каталога файла, который делает include;
- циклы (`a` → `b` → `a`) — ошибка;
- повторный include того же файла пропускается;
- глубина вложенности ограничена 32 уровнями.

## Что переносится

| dhcpd.conf | GoDHCP |
|------------|--------|
| `authoritative` | `server.authoritative` |
| `default-lease-time` / `max-lease-time` | `server.lease_default` / `lease_max` |
| `subnet … netmask … { range … }` | `subnets[]` |
| `option routers` | `gateway` |
| `option domain-name-servers` | `dns` |
| `option domain-name` | `domain` |
| `option code N` / известные имена | `options` |
| `host { hardware ethernet; fixed-address }` | `reservations` |
| `next-server` / `filename` | `next_server` / `boot_file` |
| `shared-network` / `group` | общие опции на вложенные subnet |

Не переносятся (и пишутся warning): `class`/`subclass`, failover peer, pool allow/deny, DDNS ISC-синтаксис, неизвестные statement.

После импорта проверьте YAML, задайте `api.auth` и при необходимости VLAN/relay вручную.

Импортируется только первый `range` каждой подсети. Проверьте наследование опций, PXE и резервации вручную: успешная конвертация не гарантирует эквивалентность исходной конфигурации. Вложенные `pool` и политики allow/deny требуют отдельного переноса.

Файл `dhcpd.leases` не импортируется. Перед запуском с тем же диапазоном нужен план сохранения занятости действующих адресов. Импортированный конфиг не содержит пользователей/API-ключей: до настройки `api.auth` управление будет открыто с ролью admin, даже при наличии `jwt_secret`.
