# Aperture

AI Gateway — единая прослойка между приложениями и LLM-провайдерами (OpenAI, Anthropic, Groq).

## Запуск

**Вариант A: без базы** — ключ задаётся в Admin-панели UI или из env:
```bash
go run ./cmd/aperture
# Открой http://localhost:5173 → ⚙ Настройки → введи OpenAI ключ
# Или: export OPENAI_API_KEY=sk-... (подхватится при старте)
```

**Вариант B: с PostgreSQL** — динамические ключи через API:
```bash
docker compose up -d
export DATABASE_URL=postgres://aperture:aperture@localhost:5432/aperture?sslmode=disable
export ADMIN_API_KEY=your-admin-secret
go run ./cmd/aperture
```

Создать ключ:
```bash
curl -X POST http://localhost:8080/admin/keys \
  -H "Authorization: Bearer your-admin-secret" \
  -H "Content-Type: application/json" \
  -d '{"aperture_key":"sk-aperture-xxx","openai_api_key":"sk-openai-...","name":"my-key"}'
```

Переменные окружения:
- `DATABASE_URL` — PostgreSQL (если задан, ключи из БД)
- `OPENAI_API_KEY` — fallback, если нет DATABASE_URL
- `OPENAI_BASE_URL` — базовый URL (по умолчанию `https://api.openai.com`)
- `ADMIN_API_KEY` — для Admin API (обязательно при DATABASE_URL)
- `PORT` — порт (по умолчанию `8080`)

## Эндпоинты

| Путь | Описание |
|------|----------|
| `GET /health` | Health check |
| `GET /ready` | Readiness |
| `GET /v1/models` | Список моделей (Bearer: aperture_key) |
| `POST /v1/chat/completions` | Chat completions (Bearer: aperture_key) |
| `GET /admin/config` | Статус ключа (configured: bool) |
| `POST /admin/config` | Задать OpenAI ключ (без auth, тестовый режим) |
| `POST /admin/keys` | Создать ключ (с PostgreSQL) |
| `GET /admin/keys` | Список ключей |
| `DELETE /admin/keys/{id}` | Удалить ключ |

## Документация

- [Архитектура](docs/ARCHITECTURE.md)
- [Auth и роли](docs/AUTH_AND_ACCESS.md)