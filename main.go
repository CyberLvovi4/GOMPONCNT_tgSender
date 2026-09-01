package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"

	"telegram-worker/internal/backup"
	"telegram-worker/internal/db"
	"telegram-worker/internal/logger"
	"telegram-worker/internal/telegram"
	"telegram-worker/internal/worker"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("⚠️ .env не загружен: %v (будут использоваться системные переменные)", err)
	} else {
		log.Printf("✅ .env загружен. APP_ID=%s, MTPROXY_ADDR=%s",
			os.Getenv("APP_ID"), os.Getenv("MTPROXY_ADDR"))
	}

	// 🌟 1. НАСТРАИВАЕМ ЛОГГЕР (в файл + консоль)
	cleanup, err := logger.Setup("./logs")
	if err != nil {
		// Если логгер не настроился, пишем в stderr и выходим
		fmt.Fprintf(os.Stderr, "❌ Ошибка инициализации логгера: %v\n", err)
		os.Exit(1)
	}
	defer cleanup() // Закроем файл при завершении

	// 1. Настраиваем корректное завершение по Ctrl+C
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("🔧 Инициализация приложения...")

	// 2. Инициализация БД с правильными PRAGMA для производительности и WAL
	dsn := "file:messages.db?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_cache_size=10000"
	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		log.Fatalf("❌ Не удалось открыть БД: %v", err)
	}

	// Проверка соединения
	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("❌ Не удалось подключиться к БД: %v", err)
	}
	defer sqlDB.Close()
	log.Println("✅ База данных подключена (WAL режим)")

	repo := db.NewRepository(sqlDB)

	// 🌟 НАСТРАИВАЕМ МЕНЕДЖЕР РЕЗЕРВНОГО КОПИРОВАНИЯ
	// Параметры: БД, папка для бэкапов, имя файла БД,
	// сколько бэкапов хранить, на сколько дней (0 = без ограничений)
	backupMgr, err := backup.NewManager(
		sqlDB,
		"./backups",   // папка для бэкапов
		"messages.db", // имя файла БД
		14,            // хранить последние 14 бэкапов
		30,            // ИЛИ удалять старше 30 дней (что сработает раньше)
	)
	if err != nil {
		log.Fatalf("❌ Не удалось инициализировать менеджер бэкапов: %v", err)
	}

	// 3. Инициализация пула Telegram-ботов через MTProxy
	// Убедитесь, что файл bots.json лежит в корневой директории при запуске
	tgPool, err := telegram.NewBotPool(ctx, "./bots.json")
	if err != nil {
		log.Fatalf("❌ Не удалось инициализировать Telegram-клиенты: %v", err)
	}
	defer tgPool.Close()

	// 4. Создание и запуск воркера
	w := worker.New(repo, tgPool)

	// Запускаем воркер в отдельной горутине, чтобы main не блокировался,
	// или вызываем w.Run(ctx) напрямую, если это единственная задача программы.
	// Прямой вызов предпочтительнее для простых CLI-утилит.

	// Запускаем воркер и бэкапы в отдельных горутинах
	go w.Run(ctx)

	// 🔄 Бэкап раз в 6 часов (настройте под свои нужды)
	go backupMgr.RunPeriodic(ctx, 6*time.Hour)

	log.Println("🟢 Система работает. Нажмите Ctrl+C для остановки.")

	// Блокируем main-горутину до получения сигнала
	<-ctx.Done()

	log.Println("⏳ Завершение работы, сохранение сессий...")
	time.Sleep(1 * time.Second) // Даем время на корректное завершение горутин
	log.Println("👋 До свидания!")

	defer func() {
		if r := recover(); r != nil {
			log.Printf("💥 Критическая ошибка (panic): %v", r)
		}
		// Если это запуск "в один клик" (нет аргументов командной строки),
		// то окно консоли закроется после нажатия Enter
		if len(os.Args) == 1 {
			log.Println("\n[Нажмите Enter для выхода...]")
			fmt.Scanln()
		}
	}()
}
