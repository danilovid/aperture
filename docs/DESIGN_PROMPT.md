# Промт для генерации дизайна Aperture (DLP Gateway)

> Скопируйте содержимое блока ниже в Claude (claude.ai / Claude Code).
> Рекомендуемая модель: Fable 5 (одним заходом) или Sonnet 5 (быстрые итерации).

---

# Задача

Спроектируй и свёрстай интерфейс для Aperture — self-hosted DLP-шлюза для AI-агентов.
Нужен интерактивный React-прототип: посадочная страница + админ-панель.
Один самодостаточный компонент, без внешних зависимостей и сетевых запросов —
данные замокай.

# Что за продукт

Aperture — прокси между приложениями/AI-агентами команды и LLM-провайдерами
(OpenAI, Anthropic, Groq). Подключается заменой base_url. Ключевая ценность:

1. **DLP-инспекция**: каждый запрос сканируется до отправки в облако — секреты
   (AWS-ключи, токены, приватные ключи), PII (email, карты, телефоны),
   кастомные стоп-слова. Действия: block / redact / alert.
2. **Журнал инцидентов**: кто, когда, что пытался отправить (маскированный фрагмент).
3. **Наблюдаемость**: токены, стоимость, латентность, ошибки по моделям и ключам.

Self-hosted, один Go-бинарник, данные не покидают контур компании.
Покупатель: CTO / тимлид / DevSecOps в командах, где работают AI-агенты
(Claude Code и т.п.). Аналоги по духу: Helicone, Portkey, Nightfall, но проще
и локальнее.

# Визуальное направление

- Настроение: security-инструмент, которому доверяют — точный, спокойный,
  «приборный». Ориентиры класса: Linear, Vercel, Grafana, Tailscale.
- Тёмная тема основная, светлая как опция; обе намеренные.
- Один акцентный цвет (предложи), нейтральная база. Семантика строгая:
  красный = blocked, янтарный = redacted/alert, зелёный = clean/allowed.
  Отдельные бейджи провайдеров (OpenAI/Anthropic/Groq).
- Метафора имени — диафрагма/объектив: «всё проходит через фокус».
- Моно-шрифт для чисел, ключей, фрагментов кода; табличные цифры.
- Никаких стоковых иллюстраций; спарклайны, статус-бейджи, плотные таблицы.

# Экран 1 — Landing

Hero: «Your agents talk to the cloud. Know what they say.» (или предложи лучше) +
подзаголовок про self-hosted DLP gateway + 2 CTA («Get started» / «GitHub»).
Секции: как работает (схема: agents → Aperture (scan) → providers), пример
подключения (code-snippet: замена base_url), 3 фичи (DLP-инспекция, журнал
инцидентов, cost tracking), блок «данные не покидают ваш контур», футер.

# Экран 2 — Dashboard / Overview (главный)

Селектор периода (24h / 7d / 30d). KPI-карточки:
Requests, DLP events (с разбивкой blocked/redacted), Total tokens, Cost USD,
Avg latency, Error rate. У карточек спарклайн и дельта к прошлому периоду.
Ниже: график timeseries (переключение метрики requests / dlp events / cost /
latency) и таблица «By model» (model, provider-бейдж, requests, tokens, cost,
avg latency).

# Экран 3 — DLP Events (ключевой для продукта)

Лента инцидентов: время, ключ/агент, модель, правило (aws-key, credit-card,
custom:project-x), действие (цветной бейдж: BLOCKED / REDACTED / ALERT),
маскированный фрагмент моноширинным (`AKIA****************`).
Фильтры: действие, правило, ключ, период. Клик по строке — детали
(полный контекст события, без раскрытия чувствительных данных).
Пустое состояние: «No incidents — your traffic is clean» с позитивным тоном.

# Экран 4 — Policies

Редактор политики per-key: группы детекторов (Secrets / PII / Custom rules)
с тумблерами и выбором действия (block / redact / alert only) на каждую группу.
Кастомные правила: список своих регексов/стоп-слов с добавлением.
Превью: «что произойдёт с таким-то текстом» — живая проверка примера.

# Экран 5 — Settings / Keys

Provider keys (OpenAI / Anthropic / Groq): masked inputs, индикатор
«configured», Save / Clear. Aperture keys: список (name, masked key,
created_at, привязанная политика) с Create / Delete и подтверждением.
Баннер, если без БД (ключи потеряются при рестарте).

# Данные для мока (реальные типы бэкенда)

StatsSummary { requests, prompt_tokens, completion_tokens, total_tokens,
  cost_usd, avg_latency_ms, error_rate }
TimeseriesBucket { ts, requests, total_tokens, cost_usd, avg_latency_ms }
ModelStat { model, provider, requests, total_tokens, cost_usd, avg_latency_ms }
LogEntry { ts, model, provider, prompt_tokens, completion_tokens, cost_usd,
  latency_ms, status_code, key_id, error }
DLPEvent { ts, key_id, model, rule, action: "blocked"|"redacted"|"alerted",
  masked_sample }
Замокай реалистично: gpt-4o-mini, claude-3-5-sonnet, llama-3.3-70b; 3–5 ключей
(ci-agent, dev-ivan, backend-prod); 10–15 DLP-событий разных типов.

# Состояния и детали

Loading (скелетоны), empty, error; респонсив desktop → mobile; доступность
(контраст, фокус, aria); toasts на действия; человеческое форматирование чисел
($0.0043, 1.2k tokens, 340 ms).

# Технические ограничения

React + TypeScript, всё в одном файле, инлайновые стили или один <style>,
без внешних библиотек/шрифтов/картинок по сети (CSP-safe). Тёмная/светлая тема
через prefers-color-scheme + ручной тумблер.

# Формат ответа

Сначала кратко: палитра, шрифты, принципы. Затем рабочий прототип.
Начни с экрана DLP Events — он главный дифференциатор продукта.
