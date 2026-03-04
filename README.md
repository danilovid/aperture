# Aperture

AI Gateway — единая прослойка между приложениями и LLM-провайдерами (OpenAI, Anthropic, Groq).

## Запуск

```bash
go run ./cmd/aperture
```

Порт по умолчанию: `8080` (переопределить через `PORT`).

## Эндпоинты

| Путь | Описание |
|------|----------|
| `GET /health` | Health check |
| `GET /ready` | Readiness |
| `GET /v1/models` | Список моделей (placeholder) |
| `POST /v1/chat/completions` | Chat completions (в разработке) |

## Документация

- [Архитектура](docs/ARCHITECTURE.md)
- [Auth и роли](docs/AUTH_AND_ACCESS.md)