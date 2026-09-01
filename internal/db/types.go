package db

import "time"

// Message представляет одну запись (задачу) из таблицы messages
type Message struct {
	ID                  int64   `db:"id"`
	BotCode             string  `db:"bot_code"`
	ChatID              int64   `db:"chat_id"`
	ChatUsername        *string `db:"chat_username"`
	MessageText         string  `db:"message_text"`
	ParseMode           *string `db:"parse_mode"`
	ReplyToMessageID    *int64  `db:"reply_to_message_id"`
	ReplyMarkup         *string `db:"reply_markup"`         // JSON строка с клавиатурой
	DisableNotification bool    `db:"disable_notification"` // SQLite 0/1 -> Go bool

	SenderUserName *string `db:"sender_user_name"`
	SenderPlace    *string `db:"sender_place"` // JSON с информацией об источнике

	Status        string `db:"status"` // new, processing, sent, failed
	AttemptNumber int    `db:"attempt_number"`
	MaxAttempts   int    `db:"max_attempts"`

	// Timestamps (храним как int64, так как в БД используется unixepoch())
	NextAttemptAt time.Time  `db:"next_attempt_at"`
	ScheduledAt   *time.Time `db:"scheduled_at"`
	CreatedAt     time.Time  `db:"created_at"`
	SentAt        *time.Time `db:"sent_at"`

	// Результат отправки
	SentMessageID     *int64  `db:"sent_message_id"`
	BytesSent         *int64  `db:"bytes_sent"`
	SendDurationMs    *int64  `db:"send_duration_ms"`
	TelegramErrorCode *int64  `db:"telegram_error_code"`
	ErrorText         *string `db:"error_text"`

	MessageHash *string `db:"message_hash"`
}

// // Helper для конвертации Unix timestamp в time.Time (если понадобится для логирования)
// func (m *Message) CreatedTime() time.Time {
// 	return time.Unix(m.CreatedAt, 0)
// }
