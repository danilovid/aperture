# Деплой Aperture на VDS

## Быстрый старт

На сервере:

```bash
# 1. Клонировать репозиторий
git clone https://github.com/danilovid/aperture.git
cd aperture

# 2. Запустить
docker compose -f docker-compose.prod.yml up -d --build

# 3. Открыть в браузере
# http://ВАШ_IP/
```

## Что получается

| Сервис  | Порт | Описание                    |
|---------|------|-----------------------------|
| Web UI  | 80   | Чат-интерфейс              |
| Aperture| 8080 | API (также доступен через `/api`) |

Все запросы к `/api/*` проксируются на Aperture.

## Настройка

### Свой домен

1. Укажи домен в DNS на IP сервера.
2. Добавь HTTPS (например, через Caddy или nginx + certbot).
3. Для API с другого домена нужен CORS (в Aperture он уже есть).

### Свой API URL

При сборке веб-части можно задать URL API:

```bash
docker compose -f docker-compose.prod.yml build \
  --build-arg VITE_APERTURE_URL=https://api.example.com web
```

### Переменные окружения Aperture

В `docker-compose.prod.yml` можно добавить, например:

```yaml
aperture:
  environment:
    PORT: 8080
    OPENAI_BASE_URL: https://api.openai.com  # опционально
```

Ключ OpenAI задаётся через Admin-панель в UI.

## Логи

```bash
docker compose -f docker-compose.prod.yml logs -f
```

## Остановка

```bash
docker compose -f docker-compose.prod.yml down
```
