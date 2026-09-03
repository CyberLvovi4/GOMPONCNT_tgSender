package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"telegram-worker/internal/db"

	"github.com/gotd/td/tg"
)

// SendResult содержит данные для обновления записи в БД после попытки отправки
type SendResult struct {
	TgMessageID int64
	BytesSent   int
	DurationMs  int
	ErrCode     int
	ErrText     string
}

// SendMessage отправляет сообщение через правильного бота, используя актуальный API gotgproto v1.0.0-beta22
func (p *BotPool) SendMessage(ctx context.Context, botCode string, msg db.Message, cleanText string, entities []tg.MessageEntityClass) SendResult {
	start := time.Now()
	result := SendResult{}

	// 1. Получаем нужного бота из пула
	client, ok := p.GetClient(botCode)
	if !ok {
		result.ErrText = fmt.Sprintf("бот с кодом '%s' не найден в конфигурации", botCode)
		result.ErrCode = -1
		slog.Error("Неизвестный бот-отправитель",
			"err", result.ErrText,
			"botName", botCode,
		)
		return result
	}

	// 2. Создаем контекст для отправки сообщений через этого клиента
	// Именно ext.Context содержит удобные хелперы вроде SendMessage
	protoCtx := client.CreateContext()

	// 3. В вашей схеме БД поле chat_id имеет тип INTEGER NOT NULL,
	// поэтому мы можем на 100% полагаться на него (оно надежнее, чем username).
	chatID := msg.ChatID

	// 4. Резолвим InputPeer из chat_id. Это гарантирует, что Telegram поймет, кому отправлять.
	inputPeer, err := protoCtx.ResolveInputPeerById(chatID)
	if err != nil {
		result.ErrText = fmt.Sprintf("не удалось резолвить peer для chat_id=%d: %v", chatID, err)
		result.ErrCode = -1
		slog.Error("Неизвестный получатель (возможно, он ещё не писал боту)",
			"err", err,
			"chatID", chatID,
		)
		return result
	}

	// 5. Формируем запрос на отправку
	req := &tg.MessagesSendMessageRequest{
		Peer:     inputPeer,
		Message:  cleanText, // <-- Используем очищенный текст (со смайлами, но без тегов)
		Entities: entities,  // <-- Передаем массив форматирования для Telegram
		RandomID: time.Now().UnixNano(),
	}

	// 6. Применяем модификаторы из БД
	if msg.ReplyToMessageID != nil {
		req.ReplyTo = &tg.InputReplyToMessage{
			ReplyToMsgID: int(*msg.ReplyToMessageID),
		}
	}

	if msg.DisableNotification {
		req.Silent = true
	}

	// 7. Отправляем сообщение
	// protoCtx.SendMessage принимает chatID (для обновления внутреннего кэша пиров) и сам запрос
	sentMsg, err := protoCtx.SendMessage(chatID, req)
	result.DurationMs = int(time.Since(start).Milliseconds())

	if err != nil {
		result.ErrText = err.Error()

		// Пытаемся извлечь нативный код ошибки Telegram (например, 400, 429)
		if rpcErr, ok := err.(interface{ Code() int }); ok {
			result.ErrCode = rpcErr.Code()
		} else {
			result.ErrCode = -1 // Общая ошибка сети или клиента
		}

		slog.Error("Ошибка отправки сообщения в ТГ",
			"botName", botCode,
			"msgID", msg.ID,
			"err", err,
		)
		return result
	}

	// 8. Обрабатываем успешный результат
	// sentMsg имеет тип *types.Message, который встраивает (embeds) *tg.Message
	if sentMsg != nil {
		result.TgMessageID = int64(sentMsg.ID)
		result.BytesSent = len(msg.MessageText)
		slog.Info("Сообщение отправлено успешно",
			"botName", botCode,
			"chatID", msg.ChatID,
			"msgID", msg.ID,
		)
	}

	return result
}
