package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"telegram-worker/internal/database"
	"telegram-worker/internal/logger"
	"telegram-worker/internal/telegram"
	"telegram-worker/internal/bitrix"
)

func main() {
	// Инициализация логгера
	logPath := "logs/worker.log"
	fileLogger, err := logger.NewFileLogger(logPath)
	if err != nil {
		log.Fatalf("Failed to initialize file logger: %v", err)
	}
	defer fileLogger.Close()

	// Инициализация базы данных
	db, err := database.NewSQLite("data/tasks.db")
	if err != nil {
		fileLogger.Error("Failed to initialize database", "error", err)
		log.Fatalf("Database initialization failed: %v", err)
	}
	defer db.Close()

	// Инициализация Telegram клиента
	tgToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if tgToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable is not set")
	}
	tgClient, err := telegram.NewClient(tgToken, fileLogger)
	if err != nil {
		fileLogger.Error("Failed to initialize Telegram client", "error", err)
		log.Fatalf("Telegram client initialization failed: %v", err)
	}

	// Инициализация Bitrix клиента
	bitrixWebhook := os.Getenv("BITRIX_WEBHOOK_URL")
	if bitrixWebhook == "" {
		log.Fatal("BITRIX_WEBHOOK_URL environment variable is not set")
	}
	bitrixClient := bitrix.NewClient(bitrixWebhook, fileLogger)

	// Интервал опроса базы данных (в секундах)
	pollInterval := 10 * time.Second
	if envInterval := os.Getenv("POLL_INTERVAL"); envInterval != "" {
		if d, err := time.ParseDuration(envInterval); err == nil {
			pollInterval = d
		}
	}

	fileLogger.Info("Worker started", 
		"poll_interval", pollInterval.String(),
		"log_path", logPath)

	// Обработка сигналов для graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Основной цикл работы
	for {
		select {
		case <-ticker.C:
			processTasks(db, tgClient, bitrixClient, fileLogger)
		case sig := <-sigChan:
			fileLogger.Info("Shutdown signal received", "signal", sig.String())
			return
		}
	}
}

func processTasks(db *database.DB, tgClient *telegram.Client, bitrixClient *bitrix.Client, logger *logger.FileLogger) {
	tasks, err := db.GetPendingTasks()
	if err != nil {
		logger.Error("Failed to get pending tasks", "error", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	logger.Info("Processing tasks", "count", len(tasks))

	successCount := 0
	failCount := 0

	for _, task := range tasks {
		err := tgClient.SendMessage(task.ChatID, task.Message)
		if err != nil {
			logger.Error("Failed to send message", 
				"task_id", task.ID,
				"chat_id", task.ChatID,
				"error", err)
			failCount++
			db.MarkTaskFailed(task.ID, err.Error())
		} else {
			logger.Info("Message sent successfully",
				"task_id", task.ID,
				"chat_id", task.ChatID)
			successCount++
			db.MarkTaskCompleted(task.ID)
		}
	}

	// Отправка отчёта в Битрикс если есть ошибки или выполнена большая пачка задач
	if failCount > 0 || successCount >= 10 {
		report := generateReport(successCount, failCount)
		err := bitrixClient.SendLog(report)
		if err != nil {
			logger.Error("Failed to send report to Bitrix", "error", err)
		} else {
			logger.Info("Report sent to Bitrix", 
				"success", successCount, 
				"failed", failCount)
		}
	}
}

func generateReport(success, failed int) string {
	return fmt.Sprintf("Telegram Worker Report\n"+
		"Time: %s\n"+
		"Successful: %d\n"+
		"Failed: %d\n",
		time.Now().Format(time.RFC3339),
		success,
		failed)
}
