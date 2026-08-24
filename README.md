# Telegram Worker

Приложение на Go для периодической отправки сообщений пользователям в Telegram на основе заданий из SQLite базы данных.

## Структура проекта

```
telegram-worker/
├── cmd/
│   └── worker/
│       └── main.go          # Точка входа приложения
├── internal/
│   ├── database/
│   │   └── database.go      # Работа с SQLite базой данных
│   ├── telegram/
│   │   └── client.go        # Клиент для отправки сообщений в Telegram
│   ├── bitrix/
│   │   └── client.go        # Клиент для отправки логов в Битрикс
│   └── logger/
│       └── logger.go        # Логгер с записью в файлы
├── configs/                  # Конфигурационные файлы (опционально)
├── logs/                     # Директория для лог-файлов
├── go.mod                    # Go модуль
└── README.md                 # Этот файл
```

## Функциональность

1. **Получение заданий из SQLite** - Приложение периодически опрашивает базу данных на наличие новых задач
2. **Отправка сообщений в Telegram** - Использует Telegram Bot API для отправки сообщений пользователям
3. **Логирование** - Все события записываются в JSON формате в файлы логов
4. **Отправка отчётов в Битрикс** - При ошибках или выполнении больших пачек задач отправляется отчёт администратору

## Переменные окружения

| Переменная | Описание | Пример |
|------------|----------|--------|
| `TELEGRAM_BOT_TOKEN` | Токен Telegram бота | `123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11` |
| `BITRIX_WEBHOOK_URL` | Webhook URL для Битрикс24 | `https://your-domain.bitrix24.ru/rest/1/webhook_id` |
| `POLL_INTERVAL` | Интервал опроса базы данных (опционально) | `10s`, `1m` |

## Установка зависимостей

```bash
go mod tidy
```

## Сборка

```bash
go build -o worker ./cmd/worker
```

## Запуск

```bash
export TELEGRAM_BOT_TOKEN="your_telegram_bot_token"
export BITRIX_WEBHOOK_URL="your_bitrix_webhook_url"
./worker
```

Или с указанием интервала опроса:

```bash
export POLL_INTERVAL="30s"
./worker
```

## Структура базы данных

Таблица `tasks`:

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | INTEGER PRIMARY KEY | Уникальный идентификатор задачи |
| `chat_id` | TEXT | ID чата получателя в Telegram |
| `message` | TEXT | Текст сообщения |
| `created_at` | DATETIME | Время создания задачи |
| `status` | TEXT | Статус задачи: `pending`, `completed`, `failed` |

## Добавление тестовой задачи

```sql
INSERT INTO tasks (chat_id, message) VALUES ('123456789', 'Привет! Это тестовое сообщение.');
```

## Логирование

Логи записываются в файл `logs/worker.log` в JSON формате:

```json
{"timestamp":"2024-01-15T10:30:00Z","level":"INFO","message":"Worker started","poll_interval":"10s"}
{"timestamp":"2024-01-15T10:30:10Z","level":"INFO","message":"Processing tasks","count":5}
{"timestamp":"2024-01-15T10:30:11Z","level":"INFO","message":"Message sent successfully","task_id":1,"chat_id":"123456789"}
```

## Отчёты в Битрикс

Отчёты отправляются в Битрикс при:
- Наличии ошибок при отправке сообщений
- Успешной отправке 10 и более сообщений за один цикл

Формат отчёта:
```
Telegram Worker Report
Time: 2024-01-15T10:30:00Z
Successful: 15
Failed: 2
```
