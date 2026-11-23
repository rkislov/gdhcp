# Обновление до v0.1.20 с сохранением настроек, аренд и DDNS

© 2026 Кислов Роман Сергеевич. Apache License 2.0.

Инструкция для одного Linux-сервера с пакетной службой systemd и SQLite. Выполняйте команды в root-сессии Bash в согласованное окно обслуживания. Во время остановки новые выдачи и продления недоступны.

Если сервер запускается с `-cluster-config`, используйте [отдельный порядок обновления кластера](cluster-operations.md). Для PostgreSQL подготовьте согласованную резервную копию средствами PostgreSQL вместо копирования SQLite.

## 1. Проверьте версию и действующие пути

```bash
sudo -i
godhcp version
systemctl cat godhcp
less /etc/godhcp/config.yml
```

Далее предполагаются конфигурация и секреты в /etc/godhcp/, база /var/lib/godhcp/leases.db и бинарник /usr/bin/godhcp. Проверьте database.dsn и ExecStart службы. При отличиях замените пути во всех командах.

База содержит аренды, идентификатор владельца DDNS и незавершённые DNS-операции. Сохранение этой базы позволяет продолжить управление прежними метками DNS. Сохраните также keytab, TSIG-ключи и сертификаты. Экспорт из веб-панели маскирует секреты и не заменяет копию файлов.

## 2. Скачайте пакет и проверьте SHA-256

```bash
mkdir -p /root/godhcp-update-v0.1.20
cd /root/godhcp-update-v0.1.20
uname -m
```

Выберите пакет своей системы и архитектуры из [релиза v0.1.20](https://github.com/rkislov/gdhcp/releases/tag/v0.1.20). Для x86_64 доступны:

- ALT p10 - godhcp-0.1.20-alt1.p10.x86_64.rpm
- ALT p11 - godhcp-0.1.20-alt1.p11.x86_64.rpm
- Astra Linux - godhcp_0.1.20-1astra_amd64.deb
- РЕД ОС - godhcp-0.1.20-1.redos.x86_64.rpm
- Ubuntu - godhcp_0.1.20-1ubuntu_amd64.deb
- CentOS Stream 9 - godhcp-0.1.20-1.el9.x86_64.rpm

Пример для ALT Linux p10 x86_64. В первой строке укажите имя выбранного пакета.

```bash
package=godhcp-0.1.20-alt1.p10.x86_64.rpm
base=https://github.com/rkislov/gdhcp/releases/download/v0.1.20
curl -fLO "$base/$package"
curl -fLO "$base/SHA256SUMS"
grep -F "  $package" SHA256SUMS > package.sha256
test "$(wc -l < package.sha256)" -eq 1 &&
sha256sum -c package.sha256
```

Продолжайте после результата OK. При сбое скачивания или проверки повторите загрузку и выясните причину. Для ARM64 выбирайте файлы arm64/aarch64.

## 3. Остановите службу и сохраните резервную копию

На время работ прекратите изменение настроек через веб-панель.

```bash
systemctl stop godhcp
systemctl is-active godhcp
```

Ожидается inactive. Проверка inactive возвращает ненулевой код. Если процесс продолжает работать, сначала выясните причину.

```bash
backup_dir="/root/godhcp-backup-$(date +%Y%m%d-%H%M%S)"
install -d -m 0700 "$backup_dir"
cp -a /etc/godhcp "$backup_dir/etc-godhcp"
cp -a /var/lib/godhcp "$backup_dir/var-lib-godhcp"
cp -a /usr/bin/godhcp "$backup_dir/godhcp.previous"
systemctl cat godhcp > "$backup_dir/godhcp.service.txt"
if test -d /etc/systemd/system/godhcp.service.d; then
  cp -a /etc/systemd/system/godhcp.service.d "$backup_dir/"
fi
printf 'Резервная копия: %s\n' "$backup_dir"
```

Запишите выведенный путь. Переменная backup_dir существует только в текущей shell-сессии. Сохраните установочный пакет предыдущей версии для восстановления пакетного состояния.

Копируется весь каталог базы вместе с возможными leases.db-wal и leases.db-shm. Если база или секреты находятся вне указанных каталогов, скопируйте и их с сохранением прав. Копию делайте после остановки всех процессов, использующих SQLite. Храните резервную копию с ограниченным доступом.

При наличии sqlite3 проверьте копию:

```bash
sqlite3 "$backup_dir/var-lib-godhcp/leases.db" \
  'PRAGMA integrity_check;'
```

Ожидается ok. При ошибке сначала разберитесь с целостностью копии.

## 4. Установите пакет поверх текущей версии

В каталоге скачивания выполните одну команду своей системы.

ALT Linux:

```bash
apt-get install "./$package"
```

Ubuntu или Astra Linux:

```bash
apt install "./$package"
```

РЕД ОС или CentOS:

```bash
dnf install "./$package"
```

Затем:

```bash
systemctl daemon-reload
/usr/bin/godhcp version
```

Ожидается godhcp 0.1.20.

## 5. Проверьте сохранность конфигурации и DDNS

```bash
diff -qr "$backup_dir/etc-godhcp" /etc/godhcp
```

Пример конфигурации может обновиться. Сравните рабочие config.yml, godhcp.env, keytab и ключи с копией. Если установщик спрашивает о замене рабочего конфига, сохраняйте свою действующую конфигурацию.

Проверьте прежний database.dsn, подсети, резервации, пользователей, JWT secret и параметры DDNS - enabled, сервер, прямую и обратные зоны, principal и keytab/key_file.

Для обновления существующих A/PTR без метки GoDHCP используется следующий параметр в существующей секции:

```yaml
ddns:
  adopt_existing: true
```

Это фрагмент настройки. Остальные значения секции сохраняются. Режим разрешает замену немаркированных A/PTR данными активных аренд. Чужие метки владения и CNAME защищены. Если флаг уже включён, сохраните его значение. Обратные зоны для /22 остаются прежними, например четыре существующие зоны /24.

## 6. Проверьте конфигурацию с окружением службы

Команда загружает godhcp.env и выполняет проверку от пользователя службы. Если у вашей службы другой пользователь, рабочий каталог или env-файл, используйте её действующие значения.

```bash
systemd-run \
  --unit="godhcp-validate-$(date +%s)" \
  --wait --pipe \
  -p User=godhcp \
  -p Group=godhcp \
  -p WorkingDirectory=/var/lib/godhcp \
  -p EnvironmentFile=/etc/godhcp/godhcp.env \
  /usr/bin/godhcp -validate -config /etc/godhcp/config.yml
```

Ожидается configuration is valid. При ошибке исправьте указанную причину перед запуском. Проверка YAML не подтверждает права на DNS-зоны и доступность Kerberos.

## 7. Запустите сервер и проверьте аренды

```bash
systemctl start godhcp
systemctl status godhcp --no-pager
journalctl -u godhcp --since "5 minutes ago" --no-pager
```

При файловом журнале используйте действующий logging.output. Пример:

```bash
tail -f /var/log/godhcp/godhcp.log
```

DHCPv4 использует UDP-порт 67, включённый DHCPv6 - 547. В веб-панели проверьте прежние подсети, резервации и действующие аренды. Проверьте продление адреса одного тестового клиента.

## 8. Проверьте DDNS

Дождитесь полного прохода сверки. Он может длиться дольше интервала опроса. GoDHCP обрабатывает до восьми клиентов одновременно и обновляет PTR после успешной A-записи соответствующего клиента.

Для одного действующего клиента задайте реальные значения:

```bash
dns_server=dc01.example.org
client_name=printer.hosts.example.org
client_ip=192.0.2.10
dig @"$dns_server" "$client_name" A +short
dig @"$dns_server" -x "$client_ip" +short
dig @"$dns_server" "_godhcp-owner.$client_name" TXT +short
```

Проверьте соответствие A и PTR текущей аренде. В журнале успешные операции обозначаются ddns updated. Уже применённые записи могут быть пропущены до очередного обновления.

YXRRSET означает невыполненное условие отсутствия записи. Проверьте adopt_existing, CNAME и метку владения конкретного имени. REFUSED требует проверки DNS ACL. Ошибка Kerberos требует проверки keytab, principal, времени и доступности KDC. Подробности в [руководстве DDNS](ddns-samba.md).

## Если запуск завершился ошибкой

Сохраните вывод systemctl status и journalctl. Резервная копия и прежний пакет позволяют подготовить восстановление. После возобновления выдачи адресов восстановление старой БД требует отдельной проверки - в ней отсутствуют новые аренды. Согласуйте восстановление БД и DNS, чтобы сохранить правильные метки владения и исключить повторную выдачу адресов.
