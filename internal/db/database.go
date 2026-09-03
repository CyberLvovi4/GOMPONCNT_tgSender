package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
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

	// GetSentCount возвращает количество успешно отправленных сообщений за период
	GetSentCount(ctx context.Context, from, to time.Time) (int, error)

	// GetFailedMessages возвращает сводку по неудачным отправкам за период
	GetFailedMessages(ctx context.Context, from, to time.Time) ([]FailedItem, error)

	// сбрасывает "зависшие" задания. вызывается при старте приложения
	ResetStuckTasks(ctx context.Context) error
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
		SET status = CASE WHEN attempt_number < max_attempts THEN 'new' ELSE 'failed' END,
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

// FailedItem представляет группу неудачных отправок
type FailedItem struct {
	Sender       string
	ChatID       int64
	ChatUsername *string
	Error        string
	Count        int
}

// GetSentCount возвращает количество успешно отправленных сообщений за период
func (r *repository) GetSentCount(ctx context.Context, from, to time.Time) (int, error) {
	query := `
		SELECT COUNT(*) FROM messages 
		WHERE status = 'sent' 
		  AND sent_at >= ? AND sent_at < ?
	`
	var count int
	err := r.db.QueryRowContext(ctx, query, from.Unix(), to.Unix()).Scan(&count)
	return count, err
}

func (r *repository) GetFailedMessages(ctx context.Context, from, to time.Time) ([]FailedItem, error) {
	query := `
		SELECT sender_user_name, chat_id, chat_username, error_text, COUNT(*) as cnt
		FROM messages 
		WHERE status = 'failed' 
		  AND created_at >= ? AND created_at < ?
		  AND error_text IS NOT NULL
		GROUP BY sender_user_name, chat_id, chat_username, error_text
		ORDER BY cnt DESC
		LIMIT 50
	`

	rows, err := r.db.QueryContext(ctx, query, from.Unix(), to.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []FailedItem
	for rows.Next() {
		var item FailedItem
		if err := rows.Scan(&item.Sender, &item.ChatID, &item.ChatUsername, &item.Error, &item.Count); err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

func (r *repository) ResetStuckTasks(ctx context.Context) error {
	query := `
        UPDATE messages 
        SET status = 'new', attempt_number = attempt_number - 1
        WHERE status = 'processing'
    `
	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return err
	}

	if count, _ := result.RowsAffected(); count > 0 {
		slog.Info("Сброшены зависшие задачи со статусом 'processing'",
			"count", count,
		)
	}

	return nil
}
