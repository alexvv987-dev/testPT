# URL Shortener

Тестовое задание для стажировки в Positive Technologies: HTTP-сервис,
который создаёт короткие ссылки и перенаправляет по ним на исходные URL.

## Быстрый запуск

Требуется Docker с поддержкой Compose.

```bash
docker compose up --build
```

После успешного запуска сервис доступен на `http://localhost:8080`, а состояние
можно проверить через `GET /healthz`.

Создание ссылки:

```bash
curl -i \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}' \
  http://localhost:8080/shorten
```

Первый запрос возвращает `201 Created`, повторный запрос с той же строкой URL —
`200 OK` и тот же код:

```json
{
  "short_url": "http://localhost:8080/abc123"
}
```

Проверка редиректа без перехода:

```bash
curl -I http://localhost:8080/abc123
```

Остановка сервиса:

```bash
docker compose down
```

Чтобы также удалить локальные данные PostgreSQL, можно явно выполнить
`docker compose down --volumes`.

## API

Полный контракт находится в [`api/openapi.yaml`](api/openapi.yaml).

| Метод | Путь | Назначение |
|---|---|---|
| `POST` | `/shorten` | Создать либо вернуть существующую короткую ссылку |
| `GET`, `HEAD` | `/{code}` | Перенаправить на исходный URL |
| `GET`, `HEAD` | `/healthz` | Проверить приложение и соединение с PostgreSQL |

Ошибки возвращаются в одном формате:

```json
{
  "error": {
    "code": "invalid_url",
    "message": "URL is invalid or not allowed"
  }
}
```

## Архитектура

```text
HTTP request
    |
    v
net/http handlers + middleware
    |
    v
shortener service ----> crypto/rand code generator
    |
    v
repository interface
    |
    v
PostgreSQL (pgxpool)
```

- HTTP-слой отвечает только за контракт, лимиты тела и преобразование ошибок.
- Сервис выполняет валидацию, генерацию кода и повторные попытки при коллизиях.
- Репозиторий атомарно разрешает конкуренцию по `code` и `original_url`.
- Goose-миграции запускаются отдельным одноразовым Compose-сервисом до приложения.
- Мигратор и приложение собираются в разные финальные образы и подключаются к PostgreSQL под разными ролями;
  runtime-роль имеет только `CONNECT`, `USAGE`, `SELECT` и `INSERT`.
- Интерфейсы находятся на стороне потребителя, поэтому ошибки и коллизии можно
  воспроизводить без PostgreSQL в unit-тестах.
- Multi-stage Dockerfile собирает статические бинарники для `scratch`-образа,
  который работает без shell и package manager от непривилегированного UID.

## Ключевые решения

### Go и `net/http`

Встроенного роутера Go достаточно для трёх небольших маршрутов. Отказ от крупного
веб-фреймворка уменьшает количество зависимостей и делает обработку запросов
явной.

### PostgreSQL

Ссылки сохраняются после перезапуска. Ограничения `PRIMARY KEY(code)` и
`UNIQUE(original_url)` обеспечивают целостность при конкурентных запросах.
Повторный URL определяется по точному совпадению исходной строки: приложение не
меняет регистр, path, query или порядок параметров.

### Генерация кодов

Код состоит из шести Base62-символов. Символы выбираются через `crypto/rand` с
rejection sampling, без modulo bias. При редкой коллизии сервис выполняет не более
10 попыток.

### Защита входных данных

- принимается только один JSON-объект размером до 16 KiB;
- URL ограничен 2048 байтами и схемами `http`/`https`, hostname должен быть ASCII;
- запрещены credentials, управляющие символы, `localhost` и явные приватные,
  loopback, link-local либо unspecified IP-адреса;
- сервер не разрешает DNS и не загружает пользовательский URL, поэтому обработка
  запроса не создаёт SSRF-вызовов;
- полный исходный URL, query и тело запроса не записываются в лог;
- `POST /shorten` защищён ограничителем 5 запросов/с с burst 10 по `RemoteAddr`.

Проверки не являются полноценным определением фишинга или вредоносного контента:
для этого потребовалась бы отдельная reputation-инфраструктура, отсутствующая в
рамках задания.

## Конфигурация

Для локального Compose доступны переменные из `.env.example`:

| Переменная | Значение по умолчанию | Назначение |
|---|---|---|
| `PUBLIC_BASE_URL` | `http://localhost:8080` | Публичная база короткой ссылки |
| `POSTGRES_DB` | `shortener` | Имя локальной БД |
| `POSTGRES_MIGRATOR_USER` | `shortener_migrator` | Владелец схемы для одноразового мигратора |
| `POSTGRES_MIGRATOR_PASSWORD` | `shortener_migrator` | Пароль мигратора только для локальной разработки |
| `POSTGRES_APP_PASSWORD` | `shortener_app` | Пароль ограниченной runtime-роли `shortener_app` |
| `DB_MAX_CONNS` | `10` | Максимум соединений приложения |
| `DB_MIN_CONNS` | `1` | Минимум соединений приложения |
| `DB_QUERY_TIMEOUT` | `3s` | Таймаут запроса к БД |
| `SHUTDOWN_TIMEOUT` | `10s` | Таймаут graceful shutdown |
| `RATE_LIMIT_RPS` | `5` | Скорость token bucket, от `0.001` до `10000` |
| `RATE_LIMIT_BURST` | `10` | Максимальный burst |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` или `error` |

Значения Compose предназначены только для локального запуска. Для внешнего
окружения `DATABASE_URL` и секреты должны передаваться безопасным способом.

## Локальная разработка и тесты

Требуется Go 1.26.6. Для unit-тестов PostgreSQL не нужен:

```bash
go test ./...
go vet ./...
```

Integration-тесты автоматически пропускаются без `TEST_DATABASE_URL`. Для их
запуска нужно применить миграции и передать адрес тестовой PostgreSQL:

```bash
DATABASE_URL="$TEST_DATABASE_URL" go run ./cmd/migrate up
go test ./internal/store
```

Race detector запускается в Linux CI:

```bash
go test -race ./...
```

Smoke-test после запуска Compose требует `curl` и `jq`:

```bash
sh scripts/smoke.sh
```

## Оценка качества

| Проверка | Результат |
|---|---|
| Unit-тесты | Пройдены |
| Integration-тесты PostgreSQL | Пройдены на реальной PostgreSQL 17 |
| Race detector | Пройден в Linux-контейнере с Go 1.26.6 |
| Покрытие бизнес-пакетов | 90,4% statements |
| Fuzz URL-валидатора | Более 800 тыс. входов без падений |
| `go vet` | Пройден |
| `govulncheck` | Известных достижимых уязвимостей не обнаружено |
| Docker Compose smoke-test | Пройден |
| Сканирование итогового образа | 0 известных CVE, Docker Scout от 02.09.2026 |

## Ограничения

- Нет пользователей, JWT, пользовательских alias, TTL и статистики переходов.
- Rate limiter хранится в памяти одного процесса и не рассчитан на несколько
  экземпляров приложения за proxy.
- Доступность исходного URL не проверяется, поскольку сервис не должен выполнять
  сетевые запросы к недоверенным адресам.
