package database

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TaskStatus представляет статус выполнения задачи
type TaskStatus struct {
	Status    string    `json:"status"` // pending, sending, completed, failed
	Timestamp int64     `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
	RetryCount int      `json:"retry_count,omitempty"`
}

// Task представляет задачу на отправку сообщения
type Task struct {
	ID             int64          `json:"id"`
	BotSender      string         `json:"bot_sender"`       // идентификатор бота-отправителя
	SenderData     string         `json:"sender_data"`      // JSON с вспомогательными данными
	RecipientUserID int64         `json:"recipient_user_id"` // user_id получателя в Telegram
	CreatedAtUnix  int64          `json:"created_at_unix"`  // unix timestamp создания задачи
	MessageText    string         `json:"message_text"`     // текст сообщения
	TaskStatusJSON string         `json:"task_status_json"` // JSON массив статусов выполнения
	StatusHistory  []TaskStatus   `json:"-"`                // распарсенная история статусов
}

// DB представляет подключение к базе данных
type DB struct {
	db *sql.DB
}

// NewSQLite создаёт новое подключение к SQLite базе данных
func NewSQLite(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Создаём таблицу задач с новой структурой
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		bot_sender TEXT NOT NULL,
		sender_data TEXT DEFAULT '{}',
		recipient_user_id INTEGER NOT NULL,
		created_at_unix INTEGER NOT NULL,
		message_text TEXT NOT NULL,
		task_status_json TEXT DEFAULT '[{"status":"pending","timestamp":0}]',
		status TEXT GENERATED ALWAYS AS (json_extract(task_status_json, '$[0].status')) VIRTUAL
	);
	CREATE INDEX IF NOT EXISTS idx_bot_sender ON tasks(bot_sender);
	CREATE INDEX IF NOT EXISTS idx_recipient_user_id ON tasks(recipient_user_id);
	CREATE INDEX IF NOT EXISTS idx_created_at_unix ON tasks(created_at_unix);
	CREATE INDEX IF NOT EXISTS idx_status ON tasks(status);
	`
	_, err = db.Exec(createTableSQL)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

// parseStatusHistory разбирает JSON историю статусов
func (t *Task) parseStatusHistory() error {
	if t.TaskStatusJSON == "" {
		t.StatusHistory = []TaskStatus{}
		return nil
	}
	return json.Unmarshal([]byte(t.TaskStatusJSON), &t.StatusHistory)
}

// GetPendingTasks возвращает задачи со статусом 'pending'
func (d *DB) GetPendingTasks() ([]Task, error) {
	query := `SELECT id, bot_sender, sender_data, recipient_user_id, created_at_unix, message_text, task_status_json 
			  FROM tasks 
			  WHERE json_extract(task_status_json, '$[0].status') = 'pending' 
			  ORDER BY created_at_unix ASC 
			  LIMIT 100`

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		err := rows.Scan(&t.ID, &t.BotSender, &t.SenderData, &t.RecipientUserID, &t.CreatedAtUnix, &t.MessageText, &t.TaskStatusJSON)
		if err != nil {
			return nil, err
		}
		if err := t.parseStatusHistory(); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}

// updateTaskStatus обновляет статус задачи, добавляя запись в историю статусов
func (d *DB) updateTaskStatus(taskID int64, status TaskStatus) error {
	// Получаем текущий JSON статусов
	var currentJSON string
	err := d.db.QueryRow(`SELECT task_status_json FROM tasks WHERE id = ?`, taskID).Scan(&currentJSON)
	if err != nil {
		return err
	}

	// Разбираем текущую историю
	var history []TaskStatus
	if currentJSON != "" {
		if err := json.Unmarshal([]byte(currentJSON), &history); err != nil {
			history = []TaskStatus{}
		}
	}

	// Добавляем новый статус в начало массива
	history = append([]TaskStatus{status}, history...)

	// Сериализуем обратно в JSON
	newJSON, err := json.Marshal(history)
	if err != nil {
		return err
	}

	// Обновляем запись в БД
	query := `UPDATE tasks SET task_status_json = ? WHERE id = ?`
	_, err = d.db.Exec(query, string(newJSON), taskID)
	return err
}

// MarkTaskSending помечает задачу как отправляемую
func (d *DB) MarkTaskSending(taskID int64) error {
	status := TaskStatus{
		Status:    "sending",
		Timestamp: time.Now().Unix(),
	}
	return d.updateTaskStatus(taskID, status)
}

// MarkTaskCompleted помечает задачу как выполненную
func (d *DB) MarkTaskCompleted(taskID int64) error {
	status := TaskStatus{
		Status:    "completed",
		Timestamp: time.Now().Unix(),
	}
	return d.updateTaskStatus(taskID, status)
}

// MarkTaskFailed помечает задачу как неудачную с указанием ошибки и количества попыток
func (d *DB) MarkTaskFailed(taskID int64, errorMsg string, retryCount int) error {
	status := TaskStatus{
		Status:     "failed",
		Timestamp:  time.Now().Unix(),
		Error:      errorMsg,
		RetryCount: retryCount,
	}
	return d.updateTaskStatus(taskID, status)
}

// CreateTask создаёт новую задачу на отправку сообщения
func (d *DB) CreateTask(botSender, senderData string, recipientUserID int64, createdAtUnix int64, messageText string) (int64, error) {
	initialStatus := []TaskStatus{
		{
			Status:    "pending",
			Timestamp: createdAtUnix,
		},
	}
	statusJSON, err := json.Marshal(initialStatus)
	if err != nil {
		return 0, err
	}

	query := `INSERT INTO tasks (bot_sender, sender_data, recipient_user_id, created_at_unix, message_text, task_status_json) VALUES (?, ?, ?, ?, ?, ?)`
	result, err := d.db.Exec(query, botSender, senderData, recipientUserID, createdAtUnix, messageText, string(statusJSON))
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// GetTaskByID возвращает задачу по ID
func (d *DB) GetTaskByID(taskID int64) (*Task, error) {
	query := `SELECT id, bot_sender, sender_data, recipient_user_id, created_at_unix, message_text, task_status_json 
			  FROM tasks 
			  WHERE id = ?`
	
	var t Task
	err := d.db.QueryRow(query, taskID).Scan(&t.ID, &t.BotSender, &t.SenderData, &t.RecipientUserID, &t.CreatedAtUnix, &t.MessageText, &t.TaskStatusJSON)
	if err != nil {
		return nil, err
	}
	
	if err := t.parseStatusHistory(); err != nil {
		return nil, err
	}
	
	return &t, nil
}

// GetTasksByBotSender возвращает задачи для конкретного бота-отправителя
func (d *DB) GetTasksByBotSender(botSender string, limit int) ([]Task, error) {
	query := `SELECT id, bot_sender, sender_data, recipient_user_id, created_at_unix, message_text, task_status_json 
			  FROM tasks 
			  WHERE bot_sender = ?
			  ORDER BY created_at_unix ASC 
			  LIMIT ?`

	rows, err := d.db.Query(query, botSender, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		err := rows.Scan(&t.ID, &t.BotSender, &t.SenderData, &t.RecipientUserID, &t.CreatedAtUnix, &t.MessageText, &t.TaskStatusJSON)
		if err != nil {
			return nil, err
		}
		if err := t.parseStatusHistory(); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}
