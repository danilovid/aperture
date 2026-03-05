# Aperture

AI Gateway — единая прослойка между приложениями и LLM-провайдерами (OpenAI, Anthropic, Groq).

## Запуск

```bash
export OPENAI_API_KEY=sk-...
go run ./cmd/aperture
```

Переменные окружения:
- `OPENAI_API_KEY` (обязательно) — ключ OpenAI
- `OPENAI_BASE_URL` — базовый URL (по умолчанию `https://api.openai.com`)
- `PORT` — порт (по умолчанию `8080`)

## Эндпоинты

| Путь | Описание |
|------|----------|
| `GET /health` | Health check |
| `GET /ready` | Readiness |
| `GET /v1/models` | Список моделей (прокси к OpenAI) |
| `POST /v1/chat/completions` | Chat completions (прокси к OpenAI, включая streaming) |

## Документация

- [Архитектура](docs/ARCHITECTURE.md)
- [Auth и роли](docs/AUTH_AND_ACCESS.md)