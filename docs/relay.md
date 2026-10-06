# DHCP relay

Агент ставит свой адрес в `giaddr` и дописывает опцию 82. Сервер отвечает на UDP `giaddr:67` и возвращает опцию 82 без изменений (RFC 3046 §2.2), вместе с вложенными уровнями sub-option 152 (RFC 4243). Ближайший к клиенту Circuit ID — внешний, он и выбирает VLAN.

Sub-option 5 (RFC 3527, link-selection) выбирает подсеть по вхождению адреса, даже если она не совпадает с сетью `giaddr`.

Доверие. Если `require_option_82` включён, пакет без подходящей пары `giaddr` и Remote ID из `trusted_relays` отбрасывается. Когда в записи заданы оба поля, совпасть должны оба. `trust_giaddr: false` не доверяет голому giaddr. `hops` больше `max_relay_hops` (по умолчанию 4) увеличивает счётчик `dhcp_relay_hops_exceeded_total` и не получает ответ.

Встроенные парсеры Circuit ID, по порядку:

| Имя | Пример |
|-----|--------|
| cisco | `Gi0/1:vlan20` |
| huawei | `slot=0;subslot=0;GE0/0/1;vlanid=30` |
| extreme | `1:2:20` (slot:port:vlan) |
| juniper | `ge-0/0/1.0:vlan-id20` |
| mikrotik | `bridge:10` |
| aruba | `vlan20` или `port1:vlan20` |
| hp | `Gi1/0/1:20`, если более ранний шаблон не подошёл |
| brocade | `1/1/1/vlan20` |
| unifi | `vlan20` |

Строку `vlan20` забирает Aruba: этот парсер стоит раньше UniFi и совпадает с тем же текстом.

Свой парсер — запись в `relay.circuit_id_parsers`: имя, regex с группой `vlan_group`, обычно `(?P<vlan>...)`. Проверка без выдачи адреса: `POST /api/v1/parsers/circuit-id/test` с `parser` и `circuit_id`.

В журнал запроса пишутся giaddr, Circuit ID, Remote ID, VLAN, выбранный пул и выданный адрес.
