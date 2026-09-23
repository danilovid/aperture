# Mutegate — AI Gateway

A single layer between applications and LLM providers (OpenAI, Anthropic, Groq).

## Tech stack

- **Go** — the main language
- **An OpenAI-compatible API** — one entry point
- Routing, fallback, rate limiting, cost tracking

## Roadmap

1. **MVP**: a proxy to OpenAI
2. Add Anthropic and Groq
3. Route by model
4. Streaming, tokens, billing

## Context for AI

When working on this project:
- Use Go 1.21+
- Follow the Go standards (Effective Go, gofmt)
- Aim for an OpenAI-compatible API, for uniformity
