package database

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Task struct {
	ID        int64
	ChatID    string
	Message   string
	CreatedAt time.Time
	Status    string
}

type DB struct {
	db *sql.DB
}

func NewSQLite(dbPath string) (*DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Создаём таблицу задач если она не существует
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS tasks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id TEXT NOT NULL,
		message TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		status TEXT DEFAULT 'pending'
	);
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

func (d *DB) GetPendingTasks() ([]Task, error) {
	query := `SELECT id, chat_id, message, created_at, status 
			  FROM tasks 
			  WHERE status = 'pending' 
			  ORDER BY created_at ASC 
			  LIMIT 100`

	rows, err := d.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		err := rows.Scan(&t.ID, &t.ChatID, &t.Message, &t.CreatedAt, &t.Status)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}

	return tasks, rows.Err()
}

func (d *DB) MarkTaskCompleted(taskID int64) error {
	query := `UPDATE tasks SET status = 'completed' WHERE id = ?`
	_, err := d.db.Exec(query, taskID)
	return err
}

func (d *DB) MarkTaskFailed(taskID int64, errorMsg string) error {
	query := `UPDATE tasks SET status = 'failed' WHERE id = ?`
	_, err := d.db.Exec(query, taskID)
	return err
}

func (d *DB) CreateTask(chatID, message string) (int64, error) {
	query := `INSERT INTO tasks (chat_id, message) VALUES (?, ?)`
	result, err := d.db.Exec(query, chatID, message)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
