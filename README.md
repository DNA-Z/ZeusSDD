# ZeusSDD

Локальный прототип рабочего пространства команды. Демонстрационные релизы и задачи включают статусы, метки SDD, связи, чаты и файловые диффы. Состояние задач и диффы загружаются из примеров, сообщения отправляются в Go API.

## Запуск

Понадобятся Go 1.25+ и Node.js, совместимый с Vite 7 (20.19+ или 22.12+).

В корне проекта соберите интерфейс:

```powershell
npm.cmd --prefix frontend ci
npm.cmd --prefix frontend run build
```

Затем откройте три терминала в корне проекта.

```powershell
go run ./cmd/workflow
```

```powershell
go run ./cmd/settings
```

```powershell
go run ./cmd/gateway
```

Откройте [ZeusSDD](http://127.0.0.1:8080). Для разработки интерфейса можно дополнительно запустить `npm.cmd --prefix frontend run dev`. Vite проксирует API через Gateway.

## PostgreSQL

По умолчанию деморежим хранит сообщения в памяти. Чтобы они сохранялись между перезапусками, запустите локальную БД и передайте строку подключения **сервису Workflow**:

```powershell
docker compose up -d --wait postgres
$env:DATABASE_URL = 'postgres://zeus:zeus@127.0.0.1:5434/zeus?sslmode=disable'
go run ./cmd/workflow
```

Настройки темы привязаны к браузеру. Передайте ту же переменную `DATABASE_URL` процессу `go run ./cmd/settings`, чтобы тема сохранялась после перезапуска сервиса. В деморежиме без PostgreSQL настройки хранятся в памяти.

Gateway по умолчанию слушает `127.0.0.1:8080`. Он направляет `/api/settings` в Settings на `127.0.0.1:8083`, а остальные запросы к API — в Workflow на `127.0.0.1:8081`.

В прототипе используются вымышленные задачи и локальные чаты без LLM. Корпоративная авторизация, роли и интеграции с Jira, Bitbucket пока не подключены. Мерж и ПСИ выполняются вручную.

## Агент в GigaCode через MCP

Сервис `cmd/mcp` отдаёт задачи и чаты ZeusSDD инструментами [Model Context Protocol](https://modelcontextprotocol.io). Модель и агентский цикл остаются на стороне GigaCode, приложение только предоставляет данные из Workflow по HTTP.

Запустите Workflow, затем MCP-сервис:

```powershell
go run ./cmd/workflow
go run ./cmd/mcp
```

Сервис слушает `127.0.0.1:8084` и отвечает на `POST /mcp` (streamable HTTP, JSON-RPC 2.0). Адрес Workflow задаётся переменной `WORKFLOW_URL`, адрес сервиса — `MCP_ADDR`.

Инструменты:

- `list_tasks` — задачи с релизом, статусом, веткой и числом изменённых файлов;
- `get_task` — сводка, роли, зависимости и диффы файлов задачи;
- `list_messages` — чат по задаче;
- `post_message` — сообщение в чат по задаче от имени пользователя.

Подключение к GigaCode в текущем проекте:

```powershell
gigacode mcp add --scope project --transport http zeussdd http://127.0.0.1:8084/mcp
```

Или вручную в `.gigacode/settings.json`:

```jsonc
{
  "mcpServers": {
    "zeussdd": {
      "httpUrl": "http://127.0.0.1:8084/mcp",
      "timeout": 30000
    }
  }
}
```

После изменения настроек перезапустите GigaCode и проверьте подключение командой `/mcp`. Сервис не содержит авторизации: `post_message` пишет в чат без проверки роли, поэтому запускать его следует только на локальной машине.

## Агент в чате задачи через ACP

`cmd/bridge` запускает GigaCode как агента по [Agent Client Protocol](https://agentclientprotocol.com) (`gigacode --acp`, stdio) и отдаёт браузеру HTTP-API со стримом событий через Server-Sent Events. Модель, инструменты и агентский цикл остаются внутри GigaCode; мост только передаёт промпты и публикует `session/update`.

```powershell
go run ./cmd/bridge
```

Переменные окружения: `BRIDGE_ADDR` (по умолчанию `127.0.0.1:8085`), `ACP_HOME` (изолированный корень состояния GigaCode), `ACP_WORKDIR` (рабочая папка агента), `GIGACODE_ROOT` (каталог установки, по умолчанию `%ProgramFiles%\GigaCode`), `MCP_URL` (адрес `cmd/mcp`, подключается как единственный разрешённый MCP-сервер).

Ручки:

| Метод | Путь | Назначение |
|---|---|---|
| `GET` | `/api/acp/health` | число открытых сессий и путь профиля |
| `POST` | `/api/acp/sessions` | запустить агента, получить `sessionId` и режимы |
| `POST` | `/api/acp/sessions/{id}/prompt` | отправить промпт, дождаться `stopReason` |
| `POST` | `/api/acp/sessions/{id}/cancel` | отменить текущий ход |
| `GET` | `/api/acp/sessions/{id}/events` | поток событий SSE (`update`, `permission`, `closed`) |

Изоляция профиля обязательна: GigaCode читает MCP-серверы, скиллы и правила разрешений из `~/.gigacode`, и без переопределения `GIGACODE_HOME` агент получил бы все интеграции пользователя — включая корпоративные. Мост создаёт отдельный профиль, где перечислены только разрешённые MCP-серверы, а `git push` и `git commit` запрещены.

