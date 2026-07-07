# Aperture — Roadmap: пивот в Self-hosted DLP Gateway for AI Agents

> Зафиксировано: июль 2026.
> Позиционирование: **прокси между AI-агентами команды и LLM-провайдерами,
> который до отправки в облако проверяет трафик на утечку секретов и PII,
> ведёт журнал инцидентов и считает расходы. Один Go-бинарник, self-hosted.**
>
> Покупатель: CTO / тимлид / DevSecOps команд, использующих AI-агентов.
> Подключение: замена base_url. Ниша: DLP для **API/агентского** трафика
> (не браузерного — там enterprise-игроки: Netskope, Palo Alto, Lasso).

---

## Epic 0 — Security hardening (блокер, до всего остального)

Security-продукт не может сам быть дырявым. Находки ревью, обязательные к фиксу:

- [x] **Auth в no-DB режиме**: `runtimeKeyStore.GetByApertureKey` принимает любой
      Bearer-токен (`internal/config/runtime.go`). Ввести реальный aperture-key
      (генерация при старте / env `APERTURE_KEY`), сравнивать токен.
- [x] **Админка закрыта по умолчанию**: пустой `ADMIN_API_KEY` сейчас = доступ без
      auth (`internal/server/handlers.go: requireAdmin`). Генерировать ключ при
      старте и печатать в лог, либо отказываться стартовать без него в prod.
- [x] **CORS**: убрать `Access-Control-Allow-Origin: *` для admin-роутов
      (allowlist / same-origin).
- [ ] Провайдер-ключи в Postgres: шифрование at-rest (AES-GCM, ключ из env);
      aperture-ключи хранить как hash.
- [x] README ↔ env рассинхрон: `OPENAI_API_KEY`/`ANTHROPIC_API_KEY`/`GROQ_API_KEY`
      задокументированы, но не читаются в `config.Load()` — подключить.
- [x] Таймауты на upstream `http.Client` во всех провайдерах.

**Готово, когда:** нельзя пройти ни один эндпоинт без валидного ключа; ключи не
лежат plain-text; README не обещает того, чего нет. ~2–3 дня.

## Epic 1 — Open-source гигиена

- [x] LICENSE (Apache 2.0 — совместимость с enterprise).
- [x] CI (GitHub Actions): build, `go vet`, tests, lint фронта.
- [x] Первые тесты: pricing, provider-роутинг, anthropic-трансляция, auth.
- [x] `/ready` проверяет доступность Postgres.
- [x] `POST /admin/keys` (в README задокументирован, в коде отсутствует).

**Готово, когда:** зелёный CI на PR, покрыты критические пути. ~2–3 дня,
частично параллельно с Epic 0.

## Epic 2 — DLP-движок (`internal/inspector`) — ядро продукта

- [x] Пакет `inspector`: `Scan(text) []Finding`, `Apply(policy, findings) Verdict`.
- [x] Детекторы-регексы (без ML в MVP):
  - Секреты: AWS keys, GitHub/GitLab tokens, private keys (`-----BEGIN`), JWT,
    generic `api_key=` — портировать правила gitleaks.
  - PII: email, телефоны, номера карт (Luhn), IBAN.
  - Custom: пользовательские регексы/стоп-слова.
- [x] Действия: `block` (403 с объяснением, upstream не вызывается),
      `redact` (замена на `[REDACTED:rule:n]`), `alert` (пропустить, записать).
- [x] Интеграция в pipeline: `handleChatCompletions` после чтения bodyBytes,
      до `resolveProviderForKey`. Сканируются `messages[].content`.
- [x] В MVP сканируем только запросы (не ответы) и только chat completions.

**Готово, когда:** запрос с AWS-ключом блокируется/редактируется согласно
политике, событие записано; латентность инспекции < 5ms на типовой запрос
(бенчмарк). ~1 неделя.

## Epic 3 — Журнал DLP-событий

- [ ] Таблица `dlp_events` в Postgres: ts, key_id, model, provider, rule, action,
      masked_sample. **Само чувствительное содержимое не хранится.**
- [x] `storage.DLPStore` (интерфейс + in-memory ring buffer; postgres — выше).
- [x] API: `GET /admin/dlp/events` (фильтры: action, rule, key_id, период,
      limit), `GET /admin/dlp/summary` (счётчики для KPI).

**Готово, когда:** события видны через API с фильтрацией. ~2–3 дня.

## Epic 4 — Политики

- [x] Модель `Policy`: группы детекторов (secrets/pii/custom) → действие;
      список кастомных правил. Привязка к aperture-key, дефолтная политика.
- [x] Хранение: таблица `dlp_policies` (JSONB) в Postgres + in-memory вариант.
- [x] API: `GET /admin/policies`, `PUT /admin/policies/default|keys/{id}`,
      `DELETE /admin/policies/keys/{id}`, `POST /admin/policies/test`
      (dry-run: «что произойдёт с этим текстом», в т.ч. с несохранённой политикой).

**Готово, когда:** разные ключи работают с разными политиками без рестарта. ~3–4 дня.

## Epic 5 — Админ-панель (web/)

Дизайн-промт: `docs/DESIGN_PROMPT.md`.

- [x] Вкладки: Overview (дашборд + DLP KPI), DLP Events (лента инцидентов
      с фильтрами и drawer-деталями), Policies (тумблеры групп, действия,
      кастомные правила, live-превью dry-run), Settings/Keys (+ Playground-чат).
- [x] Состояния: skeleton / empty / error; тёмная+светлая тема.

**Готово, когда:** полный цикл через UI: настроил политику → агент отправил
секрет → увидел инцидент в ленте. ~1 неделя.

## Epic 6 — Алерты

- [ ] Webhook на `block`/`alert`-события (generic JSON + шаблоны Slack/Telegram).
- [ ] Настройка через admin API + UI (Settings), дебаунс от штормов.

**Готово, когда:** блок-событие прилетает в Slack < чем через 5 сек. ~2 дня.

## Epic 7 — Позиционирование и запуск

- [ ] README переписать под DLP-gateway (текущее описание — generic proxy).
- [ ] Лендинг (из дизайн-промта), quickstart: «docker run → замени base_url →
      отправь тестовый секрет → увидь инцидент» за 5 минут.
- [ ] `examples/`: Claude Code / openai-sdk / curl.
- [ ] Демо-режим с сидированными данными для скриншотов.
- [ ] Пост для запуска (HN «Show HN», Reddit r/devops, r/selfhosted).

**Готово, когда:** незнакомый разработчик доходит от README до первого
пойманного инцидента за 10 минут. ~3–4 дня.

---

## Backlog (после MVP, по спросу)

- Сканирование **ответов** (стриминг — скользящее окно по SSE-чанкам).
- NER-детекторы (имена/адреса) локальной моделью; языки помимо EN/RU.
- De-redaction: восстановление placeholder'ов в ответе для redact-режима.
- Больше эндпоинтов: `/v1/embeddings`, `/v1/responses`; больше провайдеров
  (Gemini, Mistral, Ollama/vLLM, Bedrock).
- Полная Anthropic-совместимость: tools/function-calling, мультимодальный
  content, usage в non-stream ответе (сейчас теряется → cost=0).
- Rate limits и бюджеты per-key; атрибуция agent/session/task.
- Prometheus `/metrics`; версионированные миграции БД.
- SSO/OIDC для админки (enterprise-спрос).

## Порядок и вехи

```
Epic 0 ──► Epic 2 ──► Epic 3 ──► Epic 4 ──► Epic 5 ──► Epic 6 ──► Epic 7
Epic 1 ──┘ (параллельно с 0)

M1 (конец недели 1): безопасный gateway + CI + первый пойманный секрет (curl)
M2 (конец недели 2): политики + API событий — продукт работает headless
M3 (конец недели 3): UI + алерты — полный MVP
M4 (~день 25):       README/лендинг/примеры — публичный запуск
```
