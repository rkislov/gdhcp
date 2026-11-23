# GoDHCP 0.1.19: пошаговая эксплуатация кластера

© 2026 Кислов Роман Сергеевич. Apache License 2.0.

Эта инструкция связывает установку пакета, общую конфигурацию, сертификаты, fencing и systemd в единую процедуру. Команды выполняются в root-сессии Bash на явно указанном узле. Примеры используют документационные адреса: перед исполнением замените адреса, DNS-имена, интерфейсы, идентификаторы VM/BMC и пути. Рабочий VLAN 112 эта процедура не изменяет.

## 1. Выберите режим до создания каталога данных

- Два сервера: `mode: active-backup`, узлы a и b. Primary координирует начальный запуск; один сервер обслуживает клиентов, второй принимает снимки по mTLS. Fencing обязателен. Возвращение primary не вызывает автоматический failback. После перезапуска единственного оставшегося узла нужны второй участник и синхронизация.
- Три сервера: `mode: raft`, узлы a, b и c. Для работы требуется большинство, то есть минимум два узла. Один лидер обслуживает DHCP/API/DDNS. Перед переносом IP новый лидер подтверждает fencing прежнего владельца. Режим остаётся экспериментальным, несмотря на успешно пройденные VM-сценарии.
- Обычный сервер: запуск без `-cluster-config`. Параметр `ha.enabled` из старой конфигурации не заменяет ни один из кластерных режимов.

Форматы `pair.db` и Raft-каталога несовместимы. Нельзя превращать пару в Raft добавлением третьего peers-элемента, либо получать пару удалением участника из Raft. Для смены режима нужен отдельный план переноса. Автоматического импорта действующих аренд SQLite/PostgreSQL/ISC в кластер нет. Не включайте пустой кластер на пуле с живыми клиентами; сначала подготовьте выделенный пул или проверенный карантин/план миграции.

## 2. Подготовьте сеть и доступ к управлению

В примерах: a — 192.0.2.11, b — 192.0.2.12, c — 192.0.2.13, сервисный VIP — 192.0.2.10/24, интерфейс — ens192, реальный маршрутизатор — 192.0.2.1. DNS-имена peer-сертификатов: dhcp-a.example.net, dhcp-b.example.net, dhcp-c.example.net. Cluster ID — office.

На всех узлах должны совпадать версия GoDHCP и имена сетевых интерфейсов, используемые общей политикой. Нужны постоянные адреса узлов, синхронизированные часы и отдельный доступ к гипервизору/BMC, не зависящий от сервисного VIP.

Разрешите TCP 8444 только между peer-адресами. Клиентам/relay нужны UDP 67/68, для DHCPv6 — UDP 546/547 и необходимый IPv6 multicast. Веб/API на TCP 8181 разрешите только административной сети. Для DDNS нужен доступ к DNS/Kerberos согласно инструкции Samba. Не открывайте управляющий порт BMC клиентским VLAN. Настройте правила средствами принятого в вашей ОС firewall; проверьте обе стороны соединений.

VIP должен быть свободен и не должен автоматически добавляться NetworkManager, systemd-networkd или гипервизором при загрузке. Основные адреса узлов остаются постоянными. Relay направляйте на VIP, а `server.server_id` задайте равным VIP. Маршрутизатор в DHCP-опции gateway — реальный шлюз, не адрес DHCP-сервера, если он не маршрутизирует трафик.

## 3. Установите одну версию на все узлы

Скачайте пакет своей системы и архитектуры из релиза v0.1.19, а также SHA256SUMS. Пример проверки одного файла:

```bash
package=godhcp_0.1.19_linux_amd64.deb
grep -F "  $package" SHA256SUMS > package.sha256
test "$(wc -l < package.sha256)" -eq 1
sha256sum -c package.sha256
```

Установка Debian/Ubuntu:

```bash
apt install ./godhcp_0.1.19_linux_amd64.deb
apt install python3 iproute2 iputils-arping openssl
```

Установка Rocky/RHEL-подобной системы:

```bash
dnf install ./godhcp_0.1.19_linux_amd64.rpm
dnf install python3 iproute iputils openssl
```

ALT p11 имеет собственные RPM `godhcp-0.1.19-alt1.p11.<архитектура>.rpm`; используйте `apt-get install ./имя.rpm`. Для ALT p10, Astra Linux и РЕД ОС используйте пакеты и инструкции соответствующего дистрибутива. LoongArch64/RISC-V поставляются как дополнительные порты с отдельными границами проверки; e2k не входит в релиз.

Пакет создаёт пользователя godhcp, unit и каталоги, но не вводит кластер в эксплуатацию. Проверьте:

```bash
godhcp version
getent passwd godhcp
systemctl cat godhcp
ls -l /usr/libexec/godhcp/godhcp-fence-peer
ls -l /usr/libexec/godhcp/godhcp-cluster-ip
```

Для отдельного бинарника выполните ручную установку по основной инструкции администратора: пользователь, каталоги, unit, env-файл, hooks и документация также необходимы. Один ELF-файл не устанавливает службу и fencing-адаптеры. Linux-кластер нельзя запускать на macOS-бинарнике.

## 4. Выпустите сертификаты peer mTLS

Далее команды для изолированного компьютера CA, не для DHCP-узла. В примере создаётся отдельный CA; если организация уже использует PKI, запросите эквивалентные сертификаты. Приватный ключ CA не копируйте на реплики.

```bash
umask 077
mkdir -p godhcp-office-pki
cd godhcp-office-pki
openssl req -x509 -newkey rsa:3072 -nodes \
  -keyout ca.key -out ca.pem -days 3650 \
  -subj '/CN=GoDHCP office CA' \
  -addext 'basicConstraints=critical,CA:TRUE' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign'
```

Выдайте a/b, а для Raft также c. Имена в сертификате и манифесте должны совпадать; URI SAN связывает сертификат с cluster ID и node ID.

```bash
for node in a b c; do
  openssl req -new -newkey rsa:3072 -nodes \
    -keyout "$node.key" -out "$node.csr" \
    -subj "/CN=dhcp-$node.example.net"
  cat > "$node.ext" <<EXT
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth,clientAuth
subjectAltName=DNS:dhcp-$node.example.net,URI:spiffe://godhcp/cluster/office/node/$node
EXT
  openssl x509 -req -in "$node.csr" \
    -CA ca.pem -CAkey ca.key -CAcreateserial \
    -out "$node.pem" -days 365 -sha256 -extfile "$node.ext"
  openssl verify -CAfile ca.pem "$node.pem"
done
```

Безопасно передайте на каждый узел только ca.pem и его собственные node.pem/node.key. Например, на a после передачи файлов в закрытый staging-каталог:

```bash
install -d -o root -g godhcp -m 0750 /etc/godhcp/cluster-tls
install -o root -g godhcp -m 0640 ca.pem a.pem a.key \
  /etc/godhcp/cluster-tls/
openssl x509 -in /etc/godhcp/cluster-tls/a.pem \
  -noout -subject -issuer -dates -ext subjectAltName
```

Peer-обмен использует TLS 1.3 с проверкой CA, DNS SAN и точной URI identity. Эти сертификаты не включают HTTPS веб-панели: `api.tls` настраивается отдельно для имени сервисного VIP.

## 5. Подготовьте обычную конфигурацию и администратора

На всех узлах начальная общая политика должна совпадать. Локальные пути, адрес прослушивания и секреты задаются на каждой машине. Перед первым запуском создайте парольный хэш штатной командой:

```bash
read -rsp 'Пароль администратора: ' admin_password
printf '\n'
godhcp hash-password -password "$admin_password"
unset admin_password
```

Сохраните выведенный Argon2id-хэш в `password_hash`. CLI передаёт пароль аргументом процесса; выполняйте команду на доверенной административной машине. В root-owned `/etc/godhcp/godhcp.env` задайте одинаковый сильный `JWT_SECRET` на всех узлах, например вывод `openssl rand -hex 32`. Сохраните существующие переменные LDAP/DDNS, если файл уже используется; не перезаписывайте его целиком при обновлении. Права env-файла — 0600 root:root.

Минимальный пример `/etc/godhcp/config.yml` для выделенного тестового пула:

```yaml
server:
  listen: ':67'
  server_id: 192.0.2.10
  authoritative: true
  lease_default: 1h
  lease_max: 24h
ha:
  enabled: false
database:
  driver: sqlite
  dsn: /var/lib/godhcp/leases.db
api:
  listen: ':8181'
  auth:
    jwt_secret: '${JWT_SECRET}'
    users:
      - username: admin
        password_hash: 'ЗАМЕНИТЕ_НА_ARGON2ID_ХЭШ'
        role: admin
web:
  enabled: true
  path: /ui
subnets:
  - id: office-test
    network: 192.0.2.0/24
    range: [192.0.2.100, 192.0.2.150]
    gateway: 192.0.2.1
    dns: [192.0.2.53]
    domain: example.net
logging:
  level: info
```

`database.dsn` не импортируется как источник кластерных аренд. Пустые users/api_keys не дают анонимный доступ администратора. Настройте локальный аварийный аккаунт до LDAP; настройки AD/FreeIPA и разграничение по VLAN описаны в основной инструкции. Веб/API слушает только на активном сервере: используйте `http://192.0.2.10:8181/ui` в закрытой административной сети либо включите HTTPS.

При использовании Samba DDNS скопируйте на каждый узел соответствующий keytab, локальные параметры Kerberos и DDNS-секреты. Они не переносятся общей политикой. Текущие DDNS owner и очередь намерений сохраняются в кластерном состоянии. RA и IPv6-маршрутизацию обеспечивает сетевое оборудование; включение DHCPv6 не заменяет RA.

## 6. Настройте и отдельно проверьте fencing

Выберите адаптер по реальному месту запуска: Proxmox/KVM на управляющем узле гипервизора; VMware/Hyper-V через подготовленный PowerShell-шлюз; физический сервер через IPMI/Redfish. Готовые примеры и права провайдеров находятся в [инструкции fencing](fencing.md), включённой в PDF.

На каждой реплике установите `/etc/godhcp/fencing.json` с её `local_node` и целью для каждого другого участника. В паре a знает b, b знает a. В Raft каждый узел знает двух остальных. Node ID в targets должен точно совпадать с peers[].id. Для локального Proxmox/KVM адаптера выбирается конкретный UUID/VMID; на DHCP-госте обычно используется SSH к ограниченной команде на гипервизоре, а не локальный `qm` или `virsh`.

Пример структуры на a для пары, когда ограниченный SSH-шлюз уже подготовлен:

```json
{
  "local_node": "a",
  "targets": {
    "b": {
      "provider": "ssh",
      "restart_disabled": true,
      "host": "pve-b.example.net",
      "user": "godhcp-fence",
      "key_file": "/etc/godhcp/fencing/id_ed25519",
      "known_hosts": "/etc/godhcp/fencing/known_hosts"
    }
  }
}
```

`restart_disabled: true` — ваше подтверждение, что независимый HA/autostart/watchdog действительно отключён на стороне инфраструктуры; установка этого флага не меняет гипервизор. Файлы конфигурации и ключи доступны пользователю службы, но не должны быть доступны ему для изменения. SSH host keys проверьте по доверенному каналу; не отключайте StrictHostKeyChecking.

Команда ниже **выключает целевой узел b**. Выполните её только в согласованном стендовом окне, с консольным доступом, от имени службы a:

```bash
sudo -u godhcp /usr/libexec/godhcp/godhcp-fence-peer \
  --config /etc/godhcp/fencing.json b
```

Успех означает подтверждённое выключение/изоляцию VM или хоста. Проверьте также отказ: неверный UUID, недоступный BMC или неверные credentials должны давать ненулевой код, а не разрешать активацию. Положительное подтверждение одного ping/SSH и скрипт `exit 0` неприемлемы. Перед стартом кластера верните выключенные узлы контролируемо.

## 7. Создайте манифест выбранного режима

Для пары возьмите установленный пример `examples/cluster/active-backup.yml.example` и на a задайте:

```yaml
mode: active-backup
id: office
node: a
primary: a
listen: 192.0.2.11:8444
data_dir: /var/lib/godhcp/active-backup
failover_timeout: 30s
ca: /etc/godhcp/cluster-tls/ca.pem
cert: /etc/godhcp/cluster-tls/a.pem
key: /etc/godhcp/cluster-tls/a.key
peers:
  - {id: a, address: '192.0.2.11:8444', hostname: dhcp-a.example.net}
  - {id: b, address: '192.0.2.12:8444', hostname: dhcp-b.example.net}
fence: [/usr/libexec/godhcp/godhcp-fence-peer, --config, /etc/godhcp/fencing.json]
ip_up: [/usr/libexec/godhcp/godhcp-cluster-ip, up, ens192, 192.0.2.10/24]
ip_down: [/usr/libexec/godhcp/godhcp-cluster-ip, down, ens192, 192.0.2.10/24]
```

На b замените node/listen/cert/key. Primary и весь список peers, включая порядок, оставьте одинаковыми. Не задавайте `bootstrap: true`. Failover timeout от 30 с до 5 минут; это не гарантированное время восстановления, к нему добавляются fencing и запуск слушателей.

Для трёхузлового Raft возьмите тот же набор общих параметров, но замените режим и параметры координации:

```yaml
mode: raft
bootstrap: true
id: office
node: a
listen: 192.0.2.11:8444
data_dir: /var/lib/godhcp/cluster
ca: /etc/godhcp/cluster-tls/ca.pem
cert: /etc/godhcp/cluster-tls/a.pem
key: /etc/godhcp/cluster-tls/a.key
peers:
  - {id: a, address: '192.0.2.11:8444', hostname: dhcp-a.example.net}
  - {id: b, address: '192.0.2.12:8444', hostname: dhcp-b.example.net}
  - {id: c, address: '192.0.2.13:8444', hostname: dhcp-c.example.net}
fence: [/usr/libexec/godhcp/godhcp-fence-peer, --config, /etc/godhcp/fencing.json]
ip_up: [/usr/libexec/godhcp/godhcp-cluster-ip, up, ens192, 192.0.2.10/24]
ip_down: [/usr/libexec/godhcp/godhcp-cluster-ip, down, ens192, 192.0.2.10/24]
```

В Raft нет primary/failover_timeout. На b/c `bootstrap: false`; true разрешён только на a для первого создания пустого кластера. После успешного начального запуска измените и на a на false. Первый лидер не обязан быть a.

Для IPv4/IPv6 нужны два VIP. Создайте отдельные root-owned up/down wrappers по [инструкции Raft](cluster-runtime.md), вызывающие штатный IP-hook для обоих адресов; те же wrappers подходят паре. Не оставляйте IPv6 VIP постоянно назначенным на резервных узлах. Down должен попытаться удалить оба адреса даже при ошибке одного вызова и вернуть ненулевой код при ошибке.

## 8. Включите systemd после проверки файлов

Манифест хранится в `/etc/godhcp/cluster.yml`. Создайте только каталог выбранного режима. Пример пары:

```bash
install -d -o godhcp -g godhcp -m 0700 /var/lib/godhcp/active-backup
chown root:godhcp /etc/godhcp/cluster.yml /etc/godhcp/config.yml
chmod 0640 /etc/godhcp/cluster.yml /etc/godhcp/config.yml
install -d /etc/systemd/system/godhcp.service.d
install -m 0644 \
  /usr/share/doc/godhcp/examples/cluster/systemd-override.conf.example \
  /etc/systemd/system/godhcp.service.d/cluster.conf
systemctl daemon-reload
systemctl cat godhcp
```

Для Raft создайте `/var/lib/godhcp/cluster` с теми же правами. Unit должен использовать `-cluster-config`, пользователя godhcp, `KillMode=control-group` и capabilities CAP_NET_BIND_SERVICE/CAP_NET_ADMIN/CAP_NET_RAW. Не запускайте параллельно обычный standalone-демон с тем же пулом.

Для проверки с теми же переменными окружения, что читает unit:

```bash
set -a
. /etc/godhcp/godhcp.env
set +a
/usr/bin/godhcp -validate -config /etc/godhcp/config.yml \
  -cluster-config /etc/godhcp/cluster.yml
systemctl enable godhcp
systemctl start godhcp
journalctl -u godhcp -n 100 --no-pager
```

Проводите source только для своего root-owned env-файла с корректным shell-синтаксисом. Выполните запуск на всех участниках. `active (running)`/READY=1 означает готовность координатора; это не доказательство активного DHCP. На резервных API/веб могут не слушать — это штатно.

## 9. Примите кластер до подключения рабочего пула

1. Проверьте ровно одного владельца VIP: `ip -br address show dev ens192` на каждом узле. На активном должен быть `cluster active` либо `active-backup active` в журнале.
2. Через VIP откройте `http://192.0.2.10:8181/ui` и войдите настроенным администратором. Создайте тестовую аренду и убедитесь, что она видна в разделе «Аренды».
3. Продлите аренду реальным DHCP-клиентом. Для DHCPv6 отдельно проверьте прямой запрос и relay, если он используется.
4. Выключите активную VM/хост через управляющую сеть. Подтвердите fencing, перенос VIP и продление того же адреса. Для DDNS проверьте A/PTR и смену имени клиента.
5. Верните выключенный узел с прежним диском; проверьте его синхронизацию и повторите отказ в обратную сторону.
6. На выделенном стенде нарушьте только peer-связь при сохранённой клиентской сети. Убедитесь, что прежний владелец изолирован перед активацией нового.
7. Смоделируйте недоступный fencing. Неподтверждённое выключение не должно разрешать новый активный сервер. Для Raft дополнительно выключите две реплики: единственный оставшийся узел не должен отвечать DHCP.

Повторяемые QEMU-стенды: `bash scripts/qualify-active-backup.sh` для полной приёмки пары и `bash scripts/test-cluster-vm.sh` для трёх VM. Они не подключаются к рабочей LAN. Проверки своей инфраструктуры обязательны: тестовый QEMU-fencer не проверяет ваши credentials, UUID и ограничения гипервизора.

## 10. Повседневные изменения и наблюдение

Общую политику и пользователей изменяйте через веб/API активного VIP. После инициализации источником общей политики является кластерное состояние, а не первоначальный YAML. Веб-сохранение не обновляет локальный YAML; экспорт маскирует секреты. Локальные listener-настройки, JWT/LDAP/DDNS-секреты и сертификаты обслуживаются отдельно на каждом узле.

На активном `systemctl reload godhcp` применяет сохранённую общую политику. На резервном control socket может отсутствовать; ошибка reload не означает повреждение репликации. Для изменения локального listener/сертификата нужен согласованный restart. Отслеживайте срок сертификатов заранее, например `openssl x509 -checkend 2592000 -noout -in /etc/godhcp/cluster-tls/a.pem`.

```bash
systemctl status godhcp --no-pager
journalctl -u godhcp --since '-15 minutes' --no-pager
ip -br address show dev ens192
ss -lntup
```

Логи DHCP можно направить в файл согласно разделу logging основной инструкции; stdout координатора остаётся в journald. Пакет включает logrotate для штатных путей. Проверьте `logrotate -d /etc/logrotate.d/godhcp`. Не считайте отсутствие строк в веб-журнале доказательством отсутствия ошибок: на резервном веб-сервис не запущен.

Следите за заполнением файловой системы, задержкой fsync/peer-связи и сроком TLS-сертификатов. Пара передаёт полный снимок с изменениями; HTTP-попытка ограничена 3 с. Перегрузка может привести к прекращению обслуживания/fencing исправного узла. Подберите ресурсы и проверьте свою нагрузку; результаты краткого стенда не являются пределом производительности.

## 11. Обновление без смены режима

Начиная с релиза 0.1.18 доступна пара, но обычное обновление пакета не включает её. Для существующего standalone используйте [процедуру обновления](upgrade-v0.1.19.md). Для существующего кластера рекомендуется согласованное окно с полной остановкой: совместимость смешанных версий и rolling upgrade в этом релизе не заявлены.

1. Зафиксируйте активного владельца, версии, адреса и состояние клиентов. Прекратите изменения через веб. Сохраните пакеты предыдущей версии.
2. Запретите автоматический restart службы на всех узлах на время обслуживания: `systemctl mask --runtime godhcp.service` без `--now`. Согласуйте действия с внешним мониторингом и HA гипервизора.
3. В паре остановите сначала резервный, затем активный. Активный может попытаться fence резервный; остановка не заменяет режим обслуживания, отдельной команды planned switchover нет. Обе машины должны быть доступны через консоль. В Raft согласованно остановите все три, чтобы оставшийся участник не образовал большинство. Не делайте поочерёдную длительную остановку активного при работающих остальных без готовности к fencing.
4. На каждом узле подтвердите отсутствие процессов DHCP/API/DDNS и VIP. Если cleanup не завершён, изолируйте узел через проверенный fencing, прежде чем продолжить.
5. Сохраните отдельные холодные копии каждого узла, как описано ниже. Установите одинаковую новую версию на все участники, не меняя node ID, peers, режим или каталоги.
6. Проверьте права, env-файл, `systemctl cat` и `godhcp -validate`. Выполните `systemctl daemon-reload`, затем `systemctl unmask --runtime godhcp.service` и старт всех узлов. В Raft bootstrap остаётся false. В паре для восстановления нужны оба участника.
7. Проверьте владельца VIP, выдачу/продление, API и DDNS. При ошибке остановите ввод и анализируйте журналы; не удаляйте базу для повторного «чистого» запуска.

Если fencing выключил VM во время обслуживания, runtime-mask исчезнет при перезагрузке. Запускайте её контролируемо; до обновления/проверки не возвращайте в клиентскую сеть. Этот порядок не обещает обновления без перерыва.

## 12. Холодные резервные копии и восстановление

Базу нельзя копировать поверх работающего процесса. В согласованном окне остановите всю группу и подтвердите изоляцию/VIP, затем на **каждом** узле отдельно выполните:

```bash
backup_dir="/root/godhcp-backup-$(hostname)-$(date +%Y%m%d-%H%M%S)"
install -d -m 0700 "$backup_dir"
tar --acls --xattrs -cpf "$backup_dir/state.tar" \
  /etc/godhcp /var/lib/godhcp /etc/systemd/system/godhcp.service.d
/usr/bin/godhcp version > "$backup_dir/version.txt"
sha256sum "$backup_dir/state.tar" > "$backup_dir/SHA256SUMS"
```

Включите внешние keytab/CA/hook-файлы, если они находятся вне этих каталогов. Архив содержит секреты и пароли/ключи в конфигурации; храните его с ограниченным доступом. Вместе с копией сохраните пакет этой версии, node ID, режим, время остановки и список DHCP-пулов.

Восстановление выполняется при изоляции всех старых экземпляров. Каталог a возвращается только узлу a с тем же режимом и topology; каталог b — только b. Для пары восстановите согласованные данные обоих узлов, для Raft — совместимые реплики большинства. Не копируйте один каталог на все узлы и не включайте bootstrap поверх сохранённых данных.

Восстановленная старая копия не знает аренд, выданных после её создания. До включения выдачи нужно определить актуальные аренды/карантин и исключить повторное назначение адресов; автоматического безопасного восстановления из произвольного старого snapshot нет. При утрате единственной актуальной копии в degraded-паре гарантии отсутствия потерь нет.

## 13. Типовые неисправности

- Служба работает, VIP нет: проверьте кворум/синхронизацию, `cluster activation blocked`, ошибки fencing и IP-hook. У пары после одиночной перезагрузки это штатное ожидание второго узла.
- `x509`, неизвестная identity или TLS handshake error: сравните CA, срок, DNS SAN, URI `spiffe://godhcp/cluster/office/node/<id>`, часы и peer.hostname. Не обходите ошибку отключением проверки сертификата.
- `directory identity/topology changed` либо несовместимый формат: проверьте режим и локальный data_dir. Не удаляйте identity.json/pair.db/raft.db; сначала верните правильную конфигурацию.
- `replication failed` или `degraded after confirmed fencing`: посмотрите исходную сетевую ошибку, состояние диска, задержку репликации и факт выключения партнёра. Возвращайте его контролируемо с прежним диском.
- `address is outside the pool` / `no subnet for request`: проверьте giaddr, Option 82, привязку VLAN и подсеть. Нельзя исправлять это расширением чужого пула; VLAN 112 без отдельного решения не изменяйте.
- Веб не открывается на резервном: используйте VIP. `anonymous` не должен получать права admin; проверьте аутентификацию и имеющуюся сессию браузера.
- DDNS не обновляется после переключения: проверьте DNS/Kerberos, время, локальный keytab нового активного и сохранённые owner/очередь. Не удаляйте DDNS ownership-метки ради повторного создания записи.

При эскалации сохраните версии, манифест без секретов, временную шкалу, journalctl и pcap DHCP/peer-связи. Не публикуйте приватные ключи, keytab, JWT secret, API-ключи и полные архивы `/etc/godhcp`.
