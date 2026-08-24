package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
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

	// Инициализация Telegram клиента через MTProto
	appIDStr := os.Getenv("TELEGRAM_APP_ID")
	if appIDStr == "" {
		log.Fatal("TELEGRAM_APP_ID environment variable is not set")
	}
	appID, err := strconv.Atoi(appIDStr)
	if err != nil {
		log.Fatalf("Invalid TELEGRAM_APP_ID: %v", err)
	}

	appHash := os.Getenv("TELEGRAM_APP_HASH")
	if appHash == "" {
		log.Fatal("TELEGRAM_APP_HASH environment variable is not set")
	}

	proxyHost := os.Getenv("MT_PROXY_HOST")
	if proxyHost == "" {
		log.Fatal("MT_PROXY_HOST environment variable is not set")
	}

	proxyPortStr := os.Getenv("MT_PROXY_PORT")
	if proxyPortStr == "" {
		log.Fatal("MT_PROXY_PORT environment variable is not set")
	}
	proxyPort, err := strconv.Atoi(proxyPortStr)
	if err != nil {
		log.Fatalf("Invalid MT_PROXY_PORT: %v", err)
	}

	proxySecret := os.Getenv("MT_PROXY_SECRET")
	if proxySecret == "" {
		log.Fatal("MT_PROXY_SECRET environment variable is not set")
	}

	sessionPath := os.Getenv("SESSION_PATH")
	if sessionPath == "" {
		sessionPath = "data/session.json"
	}

	tgConfig := telegram.Config{
		AppID:       appID,
		AppHash:     appHash,
		ProxyHost:   proxyHost,
		ProxyPort:   proxyPort,
		ProxySecret: proxySecret,
		SessionPath: sessionPath,
	}

	tgClient, err := telegram.NewClient(tgConfig, fileLogger)
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
		"log_path", logPath,
		"proxy_host", proxyHost,
		"proxy_port", proxyPort)

	// Обработка сигналов для graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Основной цикл работы
	for {
		select {
		case <-ticker.C:
			processTasks(ctx, db, tgClient, bitrixClient, fileLogger)
		case sig := <-sigChan:
			fileLogger.Info("Shutdown signal received", "signal", sig.String())
			cancel()
			return
		}
	}
}

func processTasks(ctx context.Context, db *database.DB, tgClient *telegram.Client, bitrixClient *bitrix.Client, logger *logger.FileLogger) {
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
		userID, err := strconv.ParseInt(task.ChatID, 10, 64)
		if err != nil {
			logger.Error("Invalid user ID", 
				"task_id", task.ID,
				"chat_id", task.ChatID,
				"error", err)
			failCount++
			db.MarkTaskFailed(task.ID, err.Error())
			continue
		}

		err = tgClient.SendMessage(ctx, userID, task.Message)
		if err != nil {
			logger.Error("Failed to send message", 
				"task_id", task.ID,
				"user_id", userID,
				"error", err)
			failCount++
			db.MarkTaskFailed(task.ID, err.Error())
		} else {
			logger.Info("Message sent successfully",
				"task_id", task.ID,
				"user_id", userID)
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
