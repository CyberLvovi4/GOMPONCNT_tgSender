package worker

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"telegram-worker/internal/db"
	"telegram-worker/internal/formatter"
	"telegram-worker/internal/telegram"
)

type Worker struct {
	repo        db.Repository
	tgPool      *telegram.BotPool
	batchSize   int
	pollDelay   time.Duration
	debugChatID int64 // ID чата для режима отладки
}

// New создает новый экземпляр воркера
func New(repo db.Repository, tgPool *telegram.BotPool) *Worker {
	return &Worker{
		repo:        repo,
		tgPool:      tgPool,
		batchSize:   10,              // Обрабатывать по 10 сообщений за раз
		pollDelay:   5 * time.Second, // Проверять очередь каждые 5 секунд
		debugChatID: 280792996,       // Ваш ID для отладки
	}
}

// Run запускает бесконечный цикл обработки очереди.
// Останавливается при отмене context.Context.
func (w *Worker) Run(ctx context.Context) {
	slog.Info("Воркер запущен. Ожидание задач.")

	// Делаем первую проверку сразу при старте, не дожидаясь tick
	w.processBatch(ctx)

	ticker := time.NewTicker(w.pollDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Получен сигнал остановки. Воркер завершает работу.")
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

// processBatch забирает пачку сообщений и обрабатывает их
func (w *Worker) processBatch(ctx context.Context) {
	// 1. Забираем сообщения из БД (статусы 'new' или 'test')
	msgs, err := w.repo.FetchAndLockPending(ctx, w.batchSize)
	if err != nil {
		slog.Error("Ошибка при получении задач из БД",
			"err", err,
		)
		return
	}

	if len(msgs) == 0 {
		slog.Debug("Нет задач для обработки")
		return // Очередь пуста, ничего не делаем
	}

	slog.Debug("Получены задачи для обработки",
		"count", len(msgs),
	)

	debug_mode := os.Getenv("DEBUG_MODE")
	// if debug_mode == "1" {
	// 	log.Printf("режим отладки включен")
	// } else {
	// 	log.Printf("РАБОЧИЙ РЕЖИМ!!!")
	// }

	// 2. Обрабатываем каждое сообщение
	for _, msg := range msgs {
		slog.Debug("Получена новая задача",
			"msgID", msg.ID,
			"status", msg.Status)

		// 🛠️ DEBUG OVERRIDE: Если статус 'test', подменяем получателя
		if (debug_mode == "1") || strings.EqualFold(strings.TrimSpace(msg.Status), "test") {
			slog.Info("[DEBUG MODE] Перехват сообщения",
				"msgID", msg.ID,
				"original_chatID", msg.ChatID,
				"debug_chatID", w.debugChatID,
			)

			msg.ChatID = w.debugChatID
			msg.ChatUsername = nil // Принудительно отправляем по ID, игнорируя username
		}

		// 🔄 ФОРМАТИРОВАНИЕ
		cleanText, entities, err := formatter.ParseHTMLToTelegram(msg.MessageText)
		if err != nil {
			slog.Error("Ошибка парсинга HTML. Отправляем как plain text.",
				"msgID", msg.ID,
				"err", err,
			)
			cleanText = msg.MessageText // Fallback на сырой текст
			entities = nil
		}

		// Передаем очищенный текст и сущности в пул отправки
		result := w.tgPool.SendMessage(ctx, msg.BotCode, msg, cleanText, entities)

		// 4. Обновляем статус в БД в зависимости от результата
		if result.ErrCode == 0 { // 0 означает успех в нашей структуре SendResult
			err = w.repo.MarkAsSent(ctx, msg.ID, result.TgMessageID, result.DurationMs, result.BytesSent)
			if err != nil {
				slog.Error("Не удалось обновить БД (успех)",
					"msgID", msg.ID,
					"err", err,
				)
			} else {
				slog.Debug("Задача успешно завершена и сохранена в БД",
					"msgID", msg.ID,
				)
			}
		} else {
			err = w.repo.MarkAsFailed(ctx, msg.ID, result.ErrCode, result.ErrText)
			if err != nil {
				slog.Error("Не удалось обновить БД (ошибка)",
					"msgID", msg.ID,
					"err", err,
				)
			} else {
				slog.Info("Задача помечена как failed",
					"msgID", msg.ID,
					"attempt", msg.AttemptNumber+1,
					"maxAttempts", msg.MaxAttempts,
					"err", result.ErrText,
				)
			}
		}

		// Небольшая пауза между сообщениями, чтобы не спамить Telegram API слишком агрессивно
		time.Sleep(100 * time.Millisecond)
	}
}
