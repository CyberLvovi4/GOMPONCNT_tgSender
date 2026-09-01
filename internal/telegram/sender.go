package telegram

import (
	"context"
	"fmt"
	"log"
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
		log.Printf("❌ %s", result.ErrText)
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
		log.Printf("❌ %s", result.ErrText)
		return result
	}

	/////////////////////////////////////////////////////////////////////////////////////////////////////

	// // 1. Преобразуем итоговый текст в UTF-16 для точного подсчета длины, как это делает Telegram
	// utf16Encoded := utf16.Encode([]rune(cleanText))
	// maxLen := len(utf16Encoded)

	// fmt.Printf("🔍 ОТЛАДКА ГРАНИЦ:\n")
	// fmt.Printf("Итоговый текст для отправки (длина в UTF-16): %d\n", maxLen)
	// fmt.Printf("Содержимое текста: %q\n\n", cleanText)

	// // 2. Проверяем каждую сущность
	// for i, e := range entities {
	// 	var offset, length int
	// 	var typeName string

	// 	switch v := e.(type) {
	// 	case *tg.MessageEntityBold:
	// 		offset, length = v.Offset, v.Length
	// 		typeName = "Bold"
	// 	case *tg.MessageEntityCode:
	// 		offset, length = v.Offset, v.Length
	// 		typeName = "Code"
	// 	// Добавьте сюда другие типы, если используете (Italic, Underline, Pre и т.д.)
	// 	default:
	// 		continue
	// 	}

	// 	// 3. Проверка границ: смещение не может быть < 0, длина > 0,
	// 	// а их сумма не может превышать общую длину текста в UTF-16
	// 	if offset < 0 || length <= 0 || (offset+length) > maxLen {
	// 		fmt.Printf("❌ ОШИБКА ГРАНИЦ в сущности #%d (%s):\n", i, typeName)
	// 		fmt.Printf("   Offset=%d, Length=%d. Сумма (%d) > макс. длины (%d)\n", offset, length, offset+length, maxLen)
	// 	} else {
	// 		// Если границы в порядке, выведем текст, который попадет под выделение, для визуальной проверки
	// 		highlightedText := string(utf16.Decode(utf16Encoded[offset : offset+length]))
	// 		fmt.Printf("✅ Сущность #%d (%s) ОК: Offset=%d, Length=%d. Выделяемый текст: %q\n", i, typeName, offset, length, highlightedText)
	// 	}
	// }
	// fmt.Println("-------------------")
	//////////////////////////////////////////////////////////////////////////////////////////////////////

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

		log.Printf("❌ Ошибка отправки (бот=%s, db_id=%d): %v", botCode, msg.ID, err)
		return result
	}

	// 8. Обрабатываем успешный результат
	// sentMsg имеет тип *types.Message, который встраивает (embeds) *tg.Message
	if sentMsg != nil {
		result.TgMessageID = int64(sentMsg.ID)
		result.BytesSent = len(msg.MessageText)
		log.Printf("✅ Успешно (бот=%s): db_id=%d -> tg_msg_id=%d", botCode, msg.ID, result.TgMessageID)
	}

	return result
}
