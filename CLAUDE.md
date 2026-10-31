# matterqute

Десктопный клиент Mattermost на Go и Qt6 Widgets (через MIQT).

## Стек

- Go: `go.mod` требует `go 1.26.10` — этого хочет `github.com/mattermost/mattermost/server/public` v0.6.0. Системный go 1.24 сам скачивает нужный тулчейн (`GOTOOLCHAIN=auto`).
- UI: `github.com/mappu/miqt/qt6` v0.15.0 — CGO-биндинги к Qt 6.4+ (в Debian 13 — Qt 6.8.2). Первая сборка с MIQT идёт ~10 минут (компилируются биндинги), потом быстро.
- Сервер: `model.Client4` (REST API v4) и `model.WebSocketClient` из `mattermost/server/public` (Apache-2.0).

## Сборка

Системные пакеты (Debian 13): `qt6-base-dev qt6-svg-dev qt6-image-formats-plugins build-essential pkg-config`.

```sh
go build ./cmd/matterqute
go test ./...
```

Разработка без настоящего сервера: `go run ./cmd/mmfake` (слушает `127.0.0.1:8065`; учётки `alice`/`secret` и `bob`/`secret` с MFA-кодом `123456`).

Без дисплея: `QT_QPA_PLATFORM=offscreen MATTERQUTE_SCREENSHOT=/tmp/shot.png ./matterqute` — через 3 с (`MATTERQUTE_SCREENSHOT_DELAY`) сохраняет видимые окна в `/tmp/shot-N.png` и выходит. Конфиг подменяется через `XDG_CONFIG_HOME`. `MATTERQUTE_DEBUG=1` — debug-логи (в т.ч. все события WebSocket).

## Архитектура

| Пакет | Что делает | Qt |
|---|---|---|
| `internal/mm` | вход (логин/пароль/MFA), сессия, realtime по WebSocket с переподключением | нет |
| `internal/store` | (план) состояние одного сервера: команды, каналы, непрочитанные, посты, пользователи, треды | нет |
| `internal/config` | список серверов в `os.UserConfigDir()/matterqute/config.json`; токены сессий — в системном keyring, без него — в `tokens.json` (0600) | нет |
| `internal/markdown` | (план) Mattermost markdown → подмножество HTML, которое понимает Qt rich text | нет |
| `internal/ui` | виджеты MIQT: `App` (вход ↔ главное окно), `LoginDialog`, `MainWindow` | да |
| `internal/notify` | (план) уведомления freedesktop (DBus), трей | частично |
| `internal/mmfake` | фейковый сервер для разработки и тестов | нет |
| `cmd/matterqute` | точка входа | да |
| `cmd/mmfake` | запуск `mmfake` на порту | нет |

### Потоки

- Окна создаются с `WA_DeleteOnClose`: после закрытия Go-обёртка указывает на удалённый C++-объект. Коллбеки из горутин перед обращением к виджетам проверяют, что окно живо (`MainWindow.closed`, `App.login != l`).
- Qt-объекты трогаем только из главного потока Qt — того, где создан `QApplication` (MIQT сам делает `runtime.LockOSThread`).
- `mm` и `store` живут в своих горутинах и Qt не импортируют. Из горутины в UI — только через `mainthread.Start` (неблокирующий). `mainthread.Wait` из горутины, которая держит мьютекс store, — дедлок, если главный поток в этот момент ждёт тот же мьютекс.
- После каждого (пере)подключения WebSocket (`RealtimeHandler.OnConnected`) состояние досинхронизируется по REST: события за время обрыва потеряны.

### Лента сообщений

Стартовый вариант — `QScrollArea` с виджетом на пост (`QLabel` с rich text: выделение текста, ссылки). В памяти держим окно ~200 постов, историю подгружаем страницами по 60. Упрёмся в производительность — переходим на `QListView` с делегатом.

## Соглашения

- Ошибка содержит данные, из-за которых отказали: URL сервера, операцию, HTTP-статус, id ошибки Mattermost (см. `mm.ServerError`). Пароли и токены в сообщения и логи не попадают.
- Таймстампы в логах — `YYYY-MM-DD HH:MM:SS`, местное время.

## План MVP

1. Каркас UI: `QApplication`, главное окно, диалог входа (сервер → логин и пароль → код MFA, если попросят), токен в keyring, вход по сохранённому токену при старте.
2. Команды и каналы: сайдбар, непрочитанные и упоминания, переключение.
3. Лента: история с подгрузкой, отправка, realtime (`posted`, `post_edited`, `post_deleted`), отметка прочтения (`ViewChannel`).
4. Треды (включая Collapsed Reply Threads) и реакции.
5. Markdown, вложения: превью картинок, скачивание, загрузка файлов.
6. Уведомления, трей, бейджи; несколько серверов.
