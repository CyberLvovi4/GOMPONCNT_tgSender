package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Setup инициализирует slog-логгер с дублированием в файл и консоль
func Setup(logDir string, level string) (cleanup func(), err error) {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}

	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, "sender_"+dateStr+".log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	// Парсим уровень логирования
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	// MultiWriter: пишем одновременно в stdout и файл
	multiWriter := io.MultiWriter(os.Stdout, logFile)

	// 🌟 JSON-хендлер для структурированных логов
	handler := slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level: logLevel,
		// // Добавляем время в микросекундах
		// ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
		// 	if a.Key == slog.TimeKey {
		// 		a.Value = slog.StringValue(time.Now().Format("2006-01-02T15:04:05.000Z07:00"))
		// 	}
		// 	return a
		// },
	})

	// Устанавливаем глобальный логгер
	slog.SetDefault(slog.New(handler))

	return func() { logFile.Close() }, nil
}
