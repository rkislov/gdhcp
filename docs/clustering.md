# Резервирование GoDHCP и защищённый обмен

© 2026 Кислов Роман Сергеевич. Apache License 2.0.

**Обновление 19 ноября 2026 года:** реализован экспериментальный запуск через `-cluster-config`, управление workload, внешние fencing/IP hooks и сквозной Docker-тест отказа DHCPv4-лидера. Актуальная инструкция и ограничения — [автоматический кластер](cluster-runtime.md). Разделы о промежуточных этапах ниже сохранены как история разработки.


**Состояние на 18 ноября 2026 года.** Автоматический кластер GoDHCP с прямой репликацией по TLS ещё не реализован. Его архитектура описана в [проекте кластера](clustering-design.md). Ниже приведена настройка доступного сейчас варианта с общей PostgreSQL, mTLS и ручным переключением. Одновременно работает только один процесс GoDHCP. Это резервирование DHCP-узла, а доступность самой PostgreSQL обеспечивается отдельно.

## Топология и условия

Пример использует документационные адреса. Замените их своими.

- DHCP-A — `192.0.2.11`, DHCP-B — `192.0.2.12`, сервисный IPv4 — `192.0.2.10/24` на `ens192`.
- PostgreSQL — `db.example.net`, `192.0.2.20`, TCP 5432. На обоих DHCP-узлах имя должно разрешаться одинаково.
- Пулы, резервации, relay и параметры DHCP одинаковы на A и B. Имена физических интерфейсов при необходимости адаптируются локально. VLAN 112 не изменяется.
- Между DHCP и БД используется взаимный TLS. Прямого обмена A↔B в этом варианте нет, состояние передаётся через PostgreSQL.
- Предполагается одна L2-сеть для переноса сервисного IPv4. Для другой топологии настройку маршрутизации согласуйте отдельно.
- B полностью остановлен, включая DDNS, очистку аренд и API. Режим «на B отключить только DHCP UDP, оставив демон» не подходит.

Проверьте одинаковую версию бинарников и время на узлах. PostgreSQL должна быть доступна только с разрешённых адресов. Нельзя запускать два процесса с пересекающимися пулами даже при общей БД. Текущий `ha` и Keepalived без fencing этого ограничения не снимают.

## 1. Сертификаты

Для рабочей среды запросите сертификаты у внутреннего CA. Нужны серверный сертификат БД с DNS SAN `db.example.net` и EKU `serverAuth`, а также отдельные клиентские сертификаты A и B с EKU `clientAuth`. В примере CN клиентов равны `dhcp-a` и `dhcp-b`, они сопоставляются с общей ролью БД через `pg_ident.conf`.

Следующие команды показывают выпуск через OpenSSL в новой директории на административной машине. CA key остаётся на этой машине и никогда не копируется на DHCP/DB. Команды требуют OpenSSL с поддержкой `-addext`. Ключ CA зашифрован, OpenSSL запросит пароль при выпуске.

```bash
umask 077
mkdir godhcp-pki
cd godhcp-pki
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -aes-256-cbc -out ca.key
openssl req -new -x509 -sha256 -days 3650 -key ca.key -out ca.pem \
  -subj '/CN=GoDHCP Database CA' \
  -addext 'basicConstraints=critical,CA:TRUE' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign'

openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out db.key
openssl req -new -key db.key -out db.csr -subj '/CN=db.example.net'
cat > db.ext <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:db.example.net
EXT
openssl x509 -req -in db.csr -CA ca.pem -CAkey ca.key -CAcreateserial \
  -days 365 -sha256 -extfile db.ext -out db.pem

cat > client.ext <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature
extendedKeyUsage=clientAuth
EXT
for node in dhcp-a dhcp-b; do
  openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:3072 -out "$node.key"
  openssl req -new -key "$node.key" -out "$node.csr" -subj "/CN=$node"
  openssl x509 -req -in "$node.csr" -CA ca.pem -CAkey ca.key -CAserial ca.srl \
    -days 365 -sha256 -extfile client.ext -out "$node.pem"
done
openssl verify -CAfile ca.pem -purpose sslserver -verify_hostname db.example.net db.pem
openssl verify -CAfile ca.pem -purpose sslclient dhcp-a.pem dhcp-b.pem
```

Защищённо доставьте БД только `ca.pem`, `db.pem`, `db.key`. Узлу A — `ca.pem`, `dhcp-a.pem`, `dhcp-a.key`, узлу B — свой сертификат и ключ. Приватные ключи узлов должны различаться.

## 2. PostgreSQL

Установите поддерживаемую PostgreSQL. Пути конфигурации и имя systemd-unit различаются между Debian и Rocky. Найдите действующие файлы через локальную административную сессию `sudo -u postgres psql`.

```sql
SHOW config_file;
SHOW hba_file;
SHOW ident_file;
CREATE ROLE godhcp LOGIN;
CREATE DATABASE godhcp OWNER godhcp;
\connect godhcp
GRANT USAGE, CREATE ON SCHEMA public TO godhcp;
```

Команды создания выполняются один раз для новой базы. Существующие роль и базу не удаляйте. Приложение само создаёт таблицы, поэтому роли нужны права DDL в собственной базе. Не выдавайте ей SUPERUSER.

На сервере БД установите доставленные файлы. В командах предполагается, что они находятся в текущей директории.

```bash
sudo install -d -o postgres -g postgres -m 0700 /etc/postgresql/godhcp-tls
sudo install -o postgres -g postgres -m 0600 db.key /etc/postgresql/godhcp-tls/db.key
sudo install -o postgres -g postgres -m 0644 db.pem ca.pem /etc/postgresql/godhcp-tls/
```

Добавьте в действующий `postgresql.conf`.

```ini
listen_addresses = 'localhost,192.0.2.20'
ssl = on
ssl_min_protocol_version = 'TLSv1.2'
ssl_cert_file = '/etc/postgresql/godhcp-tls/db.pem'
ssl_key_file = '/etc/postgresql/godhcp-tls/db.key'
ssl_ca_file = '/etc/postgresql/godhcp-tls/ca.pem'
```

Не отключайте `fsync` или `synchronous_commit`. На Rocky с SELinux обеспечьте доступ PostgreSQL к каталогу сертификатов штатной политикой и проверьте AVC-журнал. Не выключайте SELinux для устранения ошибки доступа.

В `pg_ident.conf` добавьте отображение индивидуальных сертификатов на общую роль.

```text
godhcp_nodes dhcp-a godhcp
godhcp_nodes dhcp-b godhcp
```

В `pg_hba.conf` вставьте следующие правила **до более широких host-правил**. Сохраните локальный административный доступ. Эти запреты касаются только пары база/роль `godhcp`.

```text
hostssl godhcp godhcp 192.0.2.11/32 cert map=godhcp_nodes
hostssl godhcp godhcp 192.0.2.12/32 cert map=godhcp_nodes
host    godhcp godhcp 0.0.0.0/0    reject
host    godhcp godhcp ::/0         reject
```

`cert` требует доверенного клиентского сертификата и сопоставления identity. Обычный пароль не обходит эту проверку. Проверьте правила через `SELECT * FROM pg_hba_file_rules WHERE error IS NOT NULL;`. Перезапустите **соответствующий экземпляр PostgreSQL** для применения listen/TLS, затем убедитесь, что TLS включён. Название unit определите через `systemctl list-units 'postgresql*'`. Ограничьте firewall TCP 5432 адресами A и B.

## 3. Подключение GoDHCP

На A установите сертификат как `node.pem`, на B повторите с файлами `dhcp-b.*`.

```bash
sudo install -d -o root -g godhcp -m 0750 /etc/godhcp/db-tls
sudo install -o godhcp -g godhcp -m 0600 dhcp-a.key /etc/godhcp/db-tls/node.key
sudo install -o root -g godhcp -m 0640 dhcp-a.pem /etc/godhcp/db-tls/node.pem
sudo install -o root -g godhcp -m 0640 ca.pem /etc/godhcp/db-tls/ca.pem
```

В `/etc/godhcp/config.yml` обоих узлов задайте фрагмент ниже. Остальные разделы сохраните. `server_id` должен быть адресом, который реально переносится и доступен клиентам. Слушатель `:67` нужен в том числе для прямого broadcast, ограничения сети задаются firewall.

```yaml
database:
  driver: postgres
  dsn: "host=db.example.net port=5432 dbname=godhcp user=godhcp sslmode=verify-full sslrootcert=/etc/godhcp/db-tls/ca.pem sslcert=/etc/godhcp/db-tls/node.pem sslkey=/etc/godhcp/db-tls/node.key connect_timeout=5"
ha:
  enabled: false
# Объедините с уже существующим разделом server, не создавайте второй ключ server.
server:
  listen: ":67"
  server_id: 192.0.2.10
```

`sslmode=verify-full` проверяет цепочку и имя `db.example.net`. Не заменяйте его на `require`, `prefer` или `disable`. API TLS на порту 8181 настраивается независимо и не защищает соединение БД.

При установленном PostgreSQL-клиенте проверьте с **каждого** узла от имени службы.

```bash
sudo -u godhcp psql 'host=db.example.net port=5432 dbname=godhcp user=godhcp sslmode=verify-full sslrootcert=/etc/godhcp/db-tls/ca.pem sslcert=/etc/godhcp/db-tls/node.pem sslkey=/etc/godhcp/db-tls/node.key connect_timeout=5' \
  -c 'SELECT current_user, ssl, version, cipher FROM pg_stat_ssl WHERE pid=pg_backend_pid();'
sudo -u godhcp godhcp -validate -config /etc/godhcp/config.yml
```

`-validate` проверяет конфигурацию, но не TLS-соединение и не кластерную готовность. GoDHCP использует pgx, поэтому дополнительно проверьте запуск самого демона на изолированном стенде.

## 4. Первичный запуск и перенос существующих аренд

На обоих DHCP-узлах сначала остановите и запретите запуск службы.

```bash
sudo systemctl disable --now godhcp
sudo systemctl mask godhcp
```

Для новой сети с пустыми пулами можно инициализировать пустую PostgreSQL первым запуском A. Для действующей сети **нельзя просто заменить SQLite DSN на пустую PostgreSQL**. Будут потеряны сведения о выданных адресах.

Встроенного переноса SQLite→PostgreSQL вместе с DHCPv6 identity, DDNS ownership и очередью в этой инструкции нет. До такого перехода остановите выдачу, сделайте согласованную резервную копию и подготовьте проверенный перенос всех таблиц/идентичностей. Если исходная БД уже PostgreSQL, используйте её, не создавая пустую замену. Не продолжайте переключение, пока действующие аренды не сверены. Перенос БД с потерей записей требует отдельного плана карантина адресов.

Настройте relay на сервисный адрес, сохранив стабильный server-id действующих клиентов. На A назначьте VIP и запустите службу. Команда `ip addr add` показана для тестового ручного переноса и не сохраняется после reboot.

```bash
sudo ip addr add 192.0.2.10/24 dev ens192
sudo arping -U -c 3 -I ens192 192.0.2.10
sudo systemctl unmask godhcp
sudo systemctl start godhcp
sudo journalctl -u godhcp -n 50 --no-pager
```

Оставьте автозапуск выключенным на обоих узлах. Для этой ручной схемы после reboot оператор сначала проверяет владельца VIP и отсутствие другого активного демона. Самостоятельный старт вернувшегося A после переключения на B недопустим. `Restart=on-failure` действующего процесса не заменяет процедуру fencing.

Проверьте DHCP через клиента, не только `/readyz`. Веб-изменения записываются в локальный конфиг A. После каждого изменения защищённо переносите общие настройки на B, сохраняя его локальные пути/интерфейсы/секреты, и выполняйте `-validate` на остановленном B. Общая БД не реплицирует YAML, LDAP/RBAC и JWT-настройки автоматически.

## 5. Переключение A → B

1. Запретите изменения конфигурации на время операции и сверяйте конфиги. Сохраните резервную копию БД и настроек.
2. При доступном A выполните `systemctl mask --now godhcp`. Убедитесь, что unit inactive, процесс завершился и нет альтернативного запуска в контейнере или вручную. Только после остановки удалите VIP через `ip addr del 192.0.2.10/24 dev ens192`.
3. Если A недоступен, подтвердите его выключение через гипервизор/BMC или полностью изолируйте его сетевой трафик к клиентам, relay, PostgreSQL и DNS. Отсутствие ping/SSH не подтверждает остановку. Если исключить старый узел нельзя, B не запускайте.
4. Убедитесь в доступности актуальной PostgreSQL и наличии аренд. При переключении самой БД не используйте отставшую асинхронную реплику как будто данные не терялись. Внешний кластер БД должен гарантировать fencing и сохранение подтверждённых транзакций.
5. На B повторите команды назначения VIP, `arping`, unmask и start из предыдущего раздела. Убедитесь, что A остаётся выключенным/изолированным.
6. На клиенте проверьте новый адрес и продление существующей аренды. Сверьте MAC/DUID, IP и сроки в БД, затем журнал DDNS. B должен продолжать использовать те же DDNS owner и IPv6 DUID.
7. Вернувшийся A оставьте masked, уберите старую VIP, обновите его конфигурацию. Возврат обслуживания делается той же процедурой B → A, без автоматического failback.

Для DHCPv6 relay аналогично переносится сервисный IPv6 и проверяется обновление соседских записей/маршрута. Используйте общую таблицу `dhcp6_identity` либо один явно заданный `dhcpv6.server_duid`. Параметры IPv6 и интерфейсы описаны в [руководстве DHCPv6](dhcpv6.md). IPv6 service IP и Neighbor Discovery требуют отдельной проверки на используемом оборудовании, приведённые выше команды `arping` относятся только к IPv4.

## 6. Ротация и диагностика

Контролируйте срок сертификатов заранее, например `openssl x509 -in /etc/godhcp/db-tls/node.pem -noout -enddate`. При плановой смене CA сначала установите bundle обоих CA на сервер БД и DHCP-узлы, затем замените сертификаты, проверяйте соединение с каждой стороны и только после этого удаляйте старый CA.

Смена клиентских сертификатов GoDHCP в этом варианте требует перезапуска для гарантированного закрытия старого пула соединений. Согласуйте окно переключения. Для отзыва скомпрометированного клиента удалите его отображение/правило, примените PostgreSQL reload и завершите уже открытые сеансы этого узла. Новый HBA/CA не прерывает существующие сеансы автоматически.

При `certificate verify failed` проверяйте CA, SAN, срок и часы. При `certificate authentication failed` проверяйте CN и `pg_ident.conf`. При `permission denied` проверяйте владельца файлов, каталоги и SELinux. При потере PostgreSQL не переключайте GoDHCP на локальную пустую SQLite.

Резервируйте PostgreSQL, конфигурации и секреты раздельно. Проверяйте восстановление. Время восстановления DHCP складывается из подтверждения остановки старого узла, переноса VIP, запуска и повторного запроса клиента. Фиксированное время автоматического переключения эта схема не обещает.

## 7. Приёмка перед эксплуатацией

На изолированном стенде проверьте выдачу и продление DHCPv4/IPv6 до и после ручного переключения, отказ PostgreSQL, перезагрузку обоих узлов без самопроизвольного двойного старта, доступность service IP, сохранение DDNS owner, неверный CA/SAN, отсутствие клиентского сертификата и отказ plaintext. Для каждого сценария сохраните журнал и pcap. Не проводите имитацию двойной выдачи в рабочем VLAN.

Проверка mTLS и доступа через используемый GoDHCP драйвер запускается отдельно командой `bash scripts/test-postgres-tls.sh`. По умолчанию используется образ `postgres:16-alpine`, другой образ можно задать через `GODHCP_TEST_POSTGRES_IMAGE`. Это изолированный Docker-тест TLS и хранения, **он не проверяет VIP, fencing или переключение DHCP по сети**.

## Источники

- [PostgreSQL TLS](https://www.postgresql.org/docs/current/ssl-tcp.html) — серверная конфигурация и права на ключи.
- [PostgreSQL certificate authentication](https://www.postgresql.org/docs/current/auth-cert.html) — сертификаты клиентов и отображение имён.
- [PostgreSQL verify-full](https://www.postgresql.org/docs/current/libpq-ssl.html) — проверка имени сервера. Совместимость GoDHCP/pgx проверяется отдельным тестом выше.

Проверено 18 ноября 2026 года на PostgreSQL 16 в Docker через pgx из текущего проекта. Получен TLS 1.3, запись IPv4-аренды и сохранение DHCPv6 DUID после повторного открытия хранилища. Подтверждены отказы для неверного CA, неверного hostname, отсутствующего сертификата, CN без отображения и незашифрованного соединения. Роль приложения в тесте не имеет SUPERUSER. Сетевой failover и отказоустойчивость PostgreSQL этим тестом не проверялись.
