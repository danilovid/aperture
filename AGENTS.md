# Aperture — AI Gateway

Единая прослойка между приложениями и LLM-провайдерами (OpenAI, Anthropic, Groq).

## Tech stack

- **Go** — основной язык
- **OpenAI-совместимый API** — единая точка входа
- Роутинг, fallback, rate limiting, cost tracking

## Roadmap

1. **MVP**: прокси к OpenAI
2. Добавить Anthropic, Groq
3. Роутинг по model
4. Streaming, токены, биллинг

## Контекст для AI

При работе с этим проектом:
- Используй Go 1.21+
- Следуй стандартам Go (effective go, gofmt)
- Стремись к OpenAI-совместимому API для унификации
