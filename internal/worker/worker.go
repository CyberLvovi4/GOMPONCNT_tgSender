package worker

import (
	"context"
	"log"
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
		debugChatID: 280792996,       // Ваш ID для отладки (из условия)
	}
}

// Run запускает бесконечный цикл обработки очереди.
// Останавливается при отмене context.Context.
func (w *Worker) Run(ctx context.Context) {
	log.Println("🚀 Воркер запущен. Ожидание задач...")

	// Делаем первую проверку сразу при старте, не дожидаясь tick
	w.processBatch(ctx)

	ticker := time.NewTicker(w.pollDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 Получен сигнал остановки. Воркер завершает работу.")
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
		log.Printf("❌ Ошибка при получении задач из БД: %v", err)
		return
	}

	if len(msgs) == 0 {
		//log.Println("Нет задач для обработки")
		return // Очередь пуста, ничего не делаем
	}

	log.Printf("📦 Получено %d задач для обработки", len(msgs))
	debug_mode := os.Getenv("DEBUG_MODE")
	if debug_mode == "1" {
		log.Printf("режим отладки включен")
	} else {
		log.Printf("РАБОЧИЙ РЕЖИМ!!!")
	}

	// 2. Обрабатываем каждое сообщение
	for _, msg := range msgs {
		log.Printf("получена задача %d со статусом %s", msg.ID, msg.Status)
		//return

		// 🛠️ DEBUG OVERRIDE: Если статус 'test', подменяем получателя
		if (debug_mode == "1") || strings.EqualFold(strings.TrimSpace(msg.Status), "test") {
			log.Printf("🔧 [DEBUG MODE] Перехват сообщения #%d. Оригинальный получатель: %d, новый: %d",
				msg.ID, msg.ChatID, w.debugChatID)

			msg.ChatID = w.debugChatID
			msg.ChatUsername = nil // Принудительно отправляем по ID, игнорируя username
		}

		// 🔄 ФОРМАТИРОВАНИЕ: 1 строка кода вместо самописного парсера!
		cleanText, entities, err := formatter.ParseHTMLToTelegram(msg.MessageText)
		if err != nil {
			log.Printf("⚠️ Ошибка парсинга HTML для msg_id=%d: %v. Отправляем как plain text.", msg.ID, err)
			cleanText = msg.MessageText // Fallback на сырой текст
			entities = nil
		}

		// Передаем очищенный текст и сущности в пул отправки
		result := w.tgPool.SendMessage(ctx, msg.BotCode, msg, cleanText, entities)

		// 4. Обновляем статус в БД в зависимости от результата
		if result.ErrCode == 0 { // 0 означает успех в нашей структуре SendResult
			err = w.repo.MarkAsSent(ctx, msg.ID, result.TgMessageID, result.DurationMs, result.BytesSent)
			if err != nil {
				log.Printf("⚠️ Не удалось обновить БД (успех) для msg_id=%d: %v", msg.ID, err)
			} else {
				log.Printf("✅ Задача #%d успешно завершена и сохранена в БД", msg.ID)
			}
		} else {
			err = w.repo.MarkAsFailed(ctx, msg.ID, result.ErrCode, result.ErrText)
			if err != nil {
				log.Printf("⚠️ Не удалось обновить БД (ошибка) для msg_id=%d: %v", msg.ID, err)
			} else {
				log.Printf("⚠️ Задача #%d помечена как failed (попытка %d/%d). Ошибка: %s",
					msg.ID, msg.AttemptNumber+1, msg.MaxAttempts, result.ErrText)
			}
		}

		// Небольшая пауза между сообщениями, чтобы не спамить Telegram API слишком агрессивно
		time.Sleep(100 * time.Millisecond)
	}
}
