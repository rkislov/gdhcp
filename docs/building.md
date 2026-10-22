# Сборка пакетов и PDF

© 2026 Кислов Роман Сергеевич. Apache License 2.0.

## Проверки перед сборкой

Нужен Go 1.26 или новее. Инструмент упаковки nFPM 2.48.0 требует Go 1.26.4; при стандартном `GOTOOLCHAIN=auto` Go загрузит нужную версию для него отдельно. Зависимости приложения от инструмента упаковки не меняются.

```bash
go test -race ./...
go vet ./...
```

## Веб-интерфейс на Tailwind CSS

Готовый `internal/webui/static/style.css` хранится в репозитории и вшивается в бинарный файл. Node.js на DHCP-сервере не нужен. После изменения шаблонов, JavaScript или `internal/webui/styles/input.css` на машине сборки выполните:

```bash
npm ci
npm run build:css
npm run test:ui
```

CSS собирается локальным Tailwind CLI с зафиксированными зависимостями из `package-lock.json`. Во время работы веб-интерфейс не загружает Tailwind, шрифты и иконки из CDN. Затем пересоберите Go-бинарник и пакеты. Команда `npm run watch:css` подходит для разработки.

## PDF

Источник инструкции - `docs/admin-guide.md`, генератор - `scripts/build-admin-pdf.py`. Нужны Python 3, пакеты `reportlab` и `pypdf` и шрифты DejaVu Sans, DejaVu Sans Bold, DejaVu Sans Mono. В Debian/Ubuntu шрифты доступны в пакете `fonts-dejavu-core`.

```bash
python3 -m venv /tmp/godhcp-pdf-env
/tmp/godhcp-pdf-env/bin/pip install reportlab pypdf
/tmp/godhcp-pdf-env/bin/python scripts/build-admin-pdf.py
```

По умолчанию шрифты читаются из `/usr/share/fonts/truetype/dejavu`. На другой системе задайте каталог через `GODHCP_PDF_FONTS`. Результат: `output/pdf/godhcp-admin-guide.pdf`. После изменения инструкции пересоберите PDF, отрисуйте через `pdftoppm` и проверьте переносы команд, кириллицу, содержание и нумерацию страниц.

## Linux-пакеты

Для бинарников Linux (amd64, 386, armv7, arm64) и macOS (amd64, arm64) выполните `make binaries`. PDF, Node.js и nFPM не нужны. Raspberry Pi с 32-битной ARMv7 ОС использует `linux_armv7`, с 64-битной ОС — `linux_arm64`. На macOS Intel используйте `darwin_amd64`, на Apple Silicon — `darwin_arm64`.

Результат: `dist/releases/<version>/godhcp_<version>_<os>_<arch>`, архивы `.tar.gz` и `SHA256SUMS`. Каждый архив содержит бинарник `godhcp`, примеры конфигурации, README, LICENSE, NOTICE и уведомления сторонних компонентов. При распространении отдельных бинарников также приложите эти лицензионные файлы. Команда `make packages` отдельно собирает пакеты Linux amd64/arm64.

Сначала подготовьте PDF. Затем из корня репозитория:

```bash
make packages
```

Скрипт собирает статические Linux-бинарники amd64 и arm64, упаковывает каждую архитектуру в `.deb`, `.rpm` и `.tar.gz`, создаёт `SHA256SUMS`. Выходной каталог: `dist/releases/<version>/`. Версия берётся из `internal/version/version.go`.

Для `.deb` и `.rpm` используется фиксированная версия nFPM через `go run`. Можно передать путь к уже установленному инструменту:

```bash
NFPM=/path/to/nfpm make packages
```

Конфигурация упаковки находится в `deploy/nfpm.yaml`. Формат и обработка `config|noreplace` описаны в [документации nFPM](https://nfpm.goreleaser.com/docs/configuration/). Пакеты содержат unit systemd, пример конфигурации, лицензию и документацию, включая PDF. Установка создаёт пользователя и каталоги, но не запускает службу. Конфиг и секреты администратор задаёт до первого старта.

Контрольные суммы позволяют проверить целостность файлов; пакеты не подписаны. Проверяйте пакеты установкой и обновлением на целевых дистрибутивах, а сетевое обслуживание - отдельно на изолированном стенде. Статическая сборка не заменяет проверки systemd, сетевых политик и возможностей ядра Linux.

Для проверки установки, переустановки, удаления и синтаксиса unit в одноразовых контейнерах Debian 12 и Rocky Linux 9 выполните `bash scripts/test-packages.sh`. Нужен работающий Docker; для второй архитектуры требуется поддержка эмуляции. Этот тест не запускает systemd как PID 1 и не проверяет DHCP на сети.

## Сетевая интеграционная проверка DHCP

Запустите `make test-dhcp-docker`. Стенд использует изолированные Linux namespace внутри Docker (`--network none`), проверяет DORA, продление, VLAN, ISC relay и SQLite после перезапуска процесса и пересоздания контейнера. Сценарий возвращает ненулевой код при сбое. Журналы, PCAP и JSON-отчёты находятся в `output/dhcp-lab/`. Требования и состав 20 проверок описаны в [инструкции администратора](admin-guide.md#повторяемый-сетевой-стенд-в-docker).

## Приёмочные испытания systemd и перезагрузки

После `make packages` выполните `bash scripts/test-systemd-vm.sh` (оба дистрибутива) либо добавьте `debian`/`rocky`. QEMU ARM64 загружает собственные ядра Debian 12 и Rocky Linux 9, systemd запускает пакетный unit, затем выполняется настоящая перезагрузка гостя. Проверяются capabilities, права, reload, отказ при занятом DHCP-порте и сохранение аренд. Отчёты и журналы: `output/systemd-lab/`. Требования и границы стенда приведены в [инструкции администратора](admin-guide.md#проверка-systemd-с-настоящей-перезагрузкой-linux).

## Гонки и покрытие Go-тестами

Для проверки гонок в выдаче аренд и управлении процессом:

```bash
go test -race ./internal/core ./internal/pool ./internal/daemon ./internal/server ./cmd/godhcp
go vet ./...
GOTOOLCHAIN=go1.26.0 go test -coverprofile=coverage.out ./...
GOTOOLCHAIN=go1.26.0 go tool cover -func=coverage.out
```

После расширения тестов 22 октября 2026 года общий показатель покрытия составил 70,5% (ранее 48,7%). Хранилище SQLite покрыто на 92,6%, HTTP API — на 83,2%, менеджер пулов — на 94,1%. Команда `make cover` проверяет общий порог ≥70% и завершается с ошибкой при его нарушении. Этот отчёт учитывает Go-тесты; сетевые проверки Scapy и испытания пакетов в виртуальных машинах в него не входят.

## Проверка лицензий

`python3 scripts/check-licenses.py` проверяет Apache-2.0 и имя автора в исходниках и метаданных. `bash scripts/update-third-party.sh` заново собирает отчёты лицензий Linux amd64/arm64 и тексты уведомлений зависимостей; требуются Go, сеть и предварительное `npm ci`. Инструмент go-licenses зафиксирован на v2.0.1. После обновления зависимостей проверьте предупреждения классификатора и дополнительные LICENSE/NOTICE в исходных модулях, затем пересоберите пакеты.
