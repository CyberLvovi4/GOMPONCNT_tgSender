package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Repository interface {
	// FetchAndLockPending забирает пачку сообщений, сразу помечая их как 'processing'
	FetchAndLockPending(ctx context.Context, limit int) ([]Message, error)

	// MarkAsSent помечает сообщение как успешно отправленное
	MarkAsSent(ctx context.Context, msgID int64, tgMsgID int64, durationMs int, bytesSent int) error

	// MarkAsFailed обрабатывает ошибку: увеличивает счетчик попыток и планирует следующий ретрай
	MarkAsFailed(ctx context.Context, msgID int64, tgErrCode int, errText string) error

	// IsDuplicate проверяет, есть ли уже такое сообщение в очереди (по хешу)
	IsDuplicate(ctx context.Context, hash string) (bool, error)
}

type repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) Repository {
	return &repository{db: db}
}

// FetchAndLockPending атомарно забирает задачи из очереди
func (r *repository) FetchAndLockPending(ctx context.Context, limit int) ([]Message, error) {
	// Используем unixepoch('now') для сравнения с next_attempt_at
	now := time.Now()

	// Используем транзакцию для атомарности (чтобы другие воркеры не забрали те же строки)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // Откатится, если не вызовем tx.Commit()

	// 1. Сначала выбираем сообщения СО СТАТУСАМИ (до обновления)
	selectQuery := `
		SELECT 
			id, bot_code, chat_id, chat_username, message_text,
			reply_to_message_id, disable_notification, status,
			attempt_number, max_attempts,
			next_attempt_at,
			scheduled_at,
			created_at,
			sent_at
		FROM messages 
		WHERE status IN ('new', 'test') 
		  AND next_attempt_at <= ?
		  AND (scheduled_at IS NULL OR scheduled_at <= ?)
		ORDER BY next_attempt_at ASC, id ASC
		LIMIT ?
	`

	rows, err := r.db.QueryContext(ctx, selectQuery, now, now, limit)
	if err != nil {
		return nil, fmt.Errorf("select pending messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	var ids []int64

	for rows.Next() {
		var m Message
		err := rows.Scan(
			&m.ID, &m.BotCode, &m.ChatID, &m.ChatUsername, &m.MessageText,
			&m.ReplyToMessageID, &m.DisableNotification, &m.Status,
			&m.AttemptNumber, &m.MaxAttempts,
			&m.NextAttemptAt, &m.ScheduledAt, &m.CreatedAt, &m.SentAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, m)
		ids = append(ids, m.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}

	// 2. Если нашли сообщения, обновляем их статус на 'processing'
	if len(ids) > 0 {
		// Строим плейсхолдеры для IN (?, ?, ...)
		placeholders := make([]string, len(ids))
		args := make([]interface{}, len(ids))
		for i, id := range ids {
			placeholders[i] = "?"
			args[i] = id
		}

		updateQuery := fmt.Sprintf(
			`UPDATE messages SET status = 'processing', attempt_number = attempt_number + 1 WHERE id IN (%s)`,
			strings.Join(placeholders, ","),
		)

		_, err = tx.ExecContext(ctx, updateQuery, args...)
		if err != nil {
			return nil, fmt.Errorf("update status to processing: %w", err)
		}
	}

	// 3. Коммитим транзакцию
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return messages, nil
}

// MarkAsSent обновляет статус после успешной отправки в Telegram
func (r *repository) MarkAsSent(ctx context.Context, msgID int64, tgMsgID int64, durationMs int, bytesSent int) error {
	nowUnix := time.Now().Unix()
	query := `
		UPDATE messages 
		SET status = 'sent', 
		    sent_at = ?, 
		    sent_message_id = ?, 
		    send_duration_ms = ?, 
		    bytes_sent = ?,
		    error_text = NULL,
		    telegram_error_code = NULL
		WHERE id = ?
	`
	_, err := r.db.ExecContext(ctx, query, nowUnix, tgMsgID, durationMs, bytesSent, msgID)
	return err
}

// MarkAsFailed планирует повторную попытку или помечает как окончательно проваленную
func (r *repository) MarkAsFailed(ctx context.Context, msgID int64, tgErrCode int, errText string) error {
	// Сначала читаем текущие данные, чтобы понять, есть ли еще попытки
	// (В идеале делать это в одной транзакции, но для простоты разделим,
	// либо используем CASE WHEN в SQL)

	nowUnix := time.Now().Unix()

	// Логика ретрая: если attempt_number < max_attempts, то статус снова 'new',
	// иначе 'failed'. Также увеличиваем задержку (например, экспоненциально)
	query := `
		UPDATE messages 
		SET status = CASE WHEN attempt_number < max_attempts THEN 'test' ELSE 'failed' END,
		    next_attempt_at = CASE WHEN attempt_number < max_attempts THEN ? + (attempt_number * 60) ELSE next_attempt_at END,
		    error_text = ?,
		    telegram_error_code = ?
		WHERE id = ?
	`
	// next_attempt_at сдвигается на (номер_попытки * 60 секунд) - простая линейная задержка
	_, err := r.db.ExecContext(ctx, query, nowUnix, errText, tgErrCode, msgID)
	return err
}

func (r *repository) IsDuplicate(ctx context.Context, hash string) (bool, error) {
	var count int
	query := `SELECT COUNT(1) FROM messages WHERE message_hash = ? AND status IN ('new', 'processing', 'sent')`
	err := r.db.QueryRowContext(ctx, query, hash).Scan(&count)
	return count > 0, err
}
