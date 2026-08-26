package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

type FileLogger struct {
	file   *os.File
	mu     sync.Mutex
	prefix string
}

func NewFileLogger(logPath string) (*FileLogger, error) {
	// Создаём директорию для логов если её нет
	if err := os.MkdirAll("logs", 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return &FileLogger{
		file:   file,
		prefix: "",
	}, nil
}

func (l *FileLogger) Close() error {
	return l.file.Close()
}

func (l *FileLogger) writeLog(level, message string, fields ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	timestamp := time.Now().Format(time.RFC3339)

	logEntry := map[string]any{
		"timestamp": timestamp,
		"level":     level,
		"message":   message,
	}

	// Добавляем дополнительные поля
	for i := 0; i < len(fields); i += 2 {
		if i+1 < len(fields) {
			key, ok := fields[i].(string)
			if ok {
				logEntry[key] = fields[i+1]
			}
		}
	}

	jsonData, err := json.Marshal(logEntry)
	if err != nil {
		fmt.Fprintf(l.file, "{\"error\": \"failed to marshal log entry\", \"original_message\": \"%s\"}\n", message)
		return
	}

	fmt.Fprintln(l.file, string(jsonData))
}

func (l *FileLogger) Info(msg string, fields ...any) {
	l.writeLog("INFO", msg, fields...)
}

func (l *FileLogger) Error(msg string, fields ...any) {
	l.writeLog("ERROR", msg, fields...)
}

func (l *FileLogger) Debug(msg string, fields ...any) {
	l.writeLog("DEBUG", msg, fields...)
}

func (l *FileLogger) Warn(msg string, fields ...any) {
	l.writeLog("WARN", msg, fields...)
}
