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

## MCP для GigaCode CLI

ACP-мост отдаёт задачи и чаты ZeusSDD инструментами [Model Context Protocol](https://modelcontextprotocol.io). Этот эндпоинт используют вкладка **GigaCode** в чате задачи и отдельно запущенный GigaCode CLI.

Запустите Workflow и ACP-мост в отдельных терминалах:

```powershell
go run ./cmd/workflow
go run ./cmd/bridge
```

Мост по умолчанию слушает `127.0.0.1:8085` и принимает локальные запросы `POST /mcp` (Streamable HTTP, JSON-RPC 2.0). Адрес моста задаётся переменной `BRIDGE_ADDR`, адрес Workflow — переменной `WORKFLOW_URL`.

Инструменты:

- `list_tasks` — задачи с релизом, статусом, веткой и числом изменённых файлов;
- `get_task` — сводка, роли, зависимости и диффы файлов задачи;
- `list_messages` — чат по задаче;
- `post_message` — сообщение в чат по задаче от имени пользователя.

Подключение к GigaCode в текущем проекте:

```powershell
gigacode mcp add --scope project --transport http zeussdd http://127.0.0.1:8085/mcp
```

Или вручную в `.gigacode/settings.json`:

```jsonc
{
  "mcpServers": {
    "zeussdd": {
      "httpUrl": "http://127.0.0.1:8085/mcp",
      "timeout": 30000
    }
  }
}
```

После изменения настроек перезапустите GigaCode и проверьте подключение командой `/mcp`. Сервис не содержит авторизации: `post_message` пишет в чат без проверки роли, поэтому запускать его следует только на локальной машине.

## Агент в чате задачи через ACP

Запустите Workflow, Settings и Gateway как описано выше. В отдельном PowerShell запустите мост. Если `gigacode` доступен в `PATH`, строка с `ZEUSSDD_GIGACODE_PATH` не нужна.

```powershell
$env:ZEUSSDD_GIGACODE_PATH = 'C:\путь с пробелами\gigacode.cmd'
go run ./cmd/bridge
```

Откройте задачу в ZeusSDD, выберите **GigaCode** рядом с **Команда** и отправьте запрос. Первый запрос передаёт ключ, название, описание и ветку задачи. Ответ появляется по мере поступления. В чате можно остановить ответ или выбрать вариант разрешения, предложенный GigaCode. При переходе к другой задаче сессия закрывается.

Мост запускает `gigacode --acp` и общается с ним по [Agent Client Protocol](https://agentclientprotocol.com/protocol/v1/transports) через `stdin` и `stdout`. Браузер получает события через Gateway по SSE. Для ZeusSDD ключ к API модели не требуется.

При открытии ACP-сессии мост передаёт GigaCode адрес MCP-сервера `zeussdd` в параметре `mcpServers`.

| Переменная | Назначение |
|---|---|
| `ZEUSSDD_GIGACODE_PATH` | Полный путь к CLI. По умолчанию используется `gigacode` из `PATH`. |
| `BRIDGE_ADDR` | Адрес моста, по умолчанию `127.0.0.1:8085`. |
| `BRIDGE_URL` | Адрес моста для Gateway, по умолчанию `http://127.0.0.1:8085`. |
| `ACP_HOME` | Отдельный каталог состояния GigaCode, по умолчанию `%APPDATA%\ZeusSDD\gigacode` в Windows. |
| `ACP_WORKDIR` | Рабочий каталог агента, по умолчанию текущий каталог процесса моста. |
| `WORKFLOW_URL` | Адрес Workflow для встроенного MCP, по умолчанию `http://127.0.0.1:8081`. |
| `MCP_URL` | Адрес внешнего MCP-сервера для ACP-сессий. При заданном адресе мост отключает встроенный эндпоинт. |

Если GigaCode запросит вход, запустите его в терминале с `GIGACODE_HOME`, равным `ACP_HOME` моста, и выполните обычный вход в CLI. Мост сохраняет настройки существующего профиля между сессиями. GigaCode также может загружать настройки из рабочего каталога, указанного в `ACP_WORKDIR`.

| Метод | Путь | Назначение |
|---|---|---|
| `GET` | `/api/acp/health` | Число открытых сессий и путь профиля. |
| `POST` | `/api/acp/sessions` | Открыть сессию. Тело `{ "taskKey": "ZEUS-214" }` привязывает контекст к задаче. |
| `POST` | `/api/acp/sessions/{id}/prompt` | Отправить `{ "text": "…" }` и дождаться `stopReason`. |
| `POST` | `/api/acp/sessions/{id}/cancel` | Остановить текущий ответ. |
| `GET` | `/api/acp/sessions/{id}/events` | Поток SSE с поддержкой `Last-Event-ID`. |
| `POST` | `/api/acp/sessions/{id}/permissions` | Передать `{ "id": "…", "optionId": "…" }`. Пустой `optionId` отменяет запрос. |
| `DELETE` | `/api/acp/sessions/{id}` | Закрыть сессию и завершить процесс. |
