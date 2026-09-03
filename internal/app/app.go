package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"telegram-worker/internal/backup"
	"telegram-worker/internal/bitrix"
	"telegram-worker/internal/db"
	"telegram-worker/internal/logger"
	scheduler "telegram-worker/internal/sheduler"
	"telegram-worker/internal/stats"
	"telegram-worker/internal/telegram"
	"telegram-worker/internal/worker"
)

// App — главный контейнер приложения со всеми зависимостями
type App struct {
	// Ресурсы
	sqlDB     *sql.DB
	repo      db.Repository
	tgPool    *telegram.BotPool
	bitrix    *bitrix.Client
	backupMgr *backup.Manager
	reporter  *stats.Reporter
	scheduler *scheduler.Scheduler
	worker    *worker.Worker

	// Управление жизненным циклом
	wg         sync.WaitGroup
	logCleanup func()
}

// Init инициализирует все компоненты приложения
func Init(ctx context.Context) (*App, error) {
	app := &App{}

	// 1. Логгер (инициализируется первым, чтобы все остальные логи писались в файл)
	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))
	if logLevel == "" {
		logLevel = "info"
	}

	cleanup, err := logger.Setup("./logs", logLevel)
	if err != nil {
		panic(fmt.Sprintf("error while setting up logger:", err))
	}
	app.logCleanup = cleanup

	// 2. База данных
	if err := app.initDatabase(); err != nil {
		return nil, fmt.Errorf("инициализация БД: %w", err)
	}

	// 2.5 сброс "зависших" задач
	if err := app.repo.ResetStuckTasks(ctx); err != nil {
		slog.Error("Ошибка сброса зависших задач",
			"err", err,
		)
	}
	// 3. Битрикс24 (опционально)
	app.initBitrix()

	// 4. Telegram-пул
	if err := app.initTelegram(ctx); err != nil {
		return nil, fmt.Errorf("инициализация Telegram: %w", err)
	}

	// 5. Менеджер бэкапов
	if err := app.initBackup(); err != nil {
		return nil, fmt.Errorf("инициализация бэкапов: %w", err)
	}

	// 6. Воркер
	app.worker = worker.New(app.repo, app.tgPool)

	// 7. Планировщик отчётов (только если Битрикс настроен)
	if app.bitrix != nil {
		app.reporter = stats.NewReporter(app.repo, app.bitrix)
		app.scheduler = scheduler.NewScheduler(app.reporter)
	}

	return app, nil
}

func (a *App) initDatabase() error {

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		return fmt.Errorf("Не указан путь к БД")
	}

	//dsn := "file:messages.db?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_cache_size=10000"

	sqlDB, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("открытие БД: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA cache_size=-10000;",
		"PRAGMA foreign_keys=ON;",
	}

	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			slog.Error("Ошибка установки PRAGMA",
				"pragma", p,
				"error", err.Error(),
			)
		}
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return fmt.Errorf("подключение к БД: %w", err)
	}

	// // Оптимизация пула соединений для SQLite (только 1 писатель)
	// sqlDB.SetMaxOpenConns(1)

	a.sqlDB = sqlDB
	a.repo = db.NewRepository(sqlDB)
	slog.Info("База данных подключена")
	return nil
}

func (a *App) initBitrix() {
	webhook := os.Getenv("BITRIX_WEBHOOK_URL")
	chatID := os.Getenv("BITRIX_CHAT_ID")

	if webhook != "" && chatID != "" {
		a.bitrix = bitrix.NewClient(webhook, chatID)
		slog.Info("Битрикс24 клиент инициализирован")
	} else {
		slog.Error("Пропущены BITRIX_WEBHOOK_URL или BITRIX_CHAT_ID в .env")
	}
}

func (a *App) initTelegram(ctx context.Context) error {
	botsFile := os.Getenv("BOTS_FILE")

	if botsFile == "" {
		return fmt.Errorf("Пропущены BOTS_FILE в .env")
	}

	pool, err := telegram.NewBotPool(ctx, botsFile)
	if err != nil {
		return err
	}
	a.tgPool = pool
	return nil
}

func (a *App) initBackup() error {
	mgr, err := backup.NewManager(
		a.sqlDB,
		"./backups",
		"messages.db",
		14, // хранить последние 14 бэкапов
		30, // ИЛИ удалять старше 30 дней
	)
	if err != nil {
		return err
	}
	a.backupMgr = mgr
	return nil
}

// Run запускает все фоновые задачи
func (a *App) Run(ctx context.Context) {
	slog.Info("Запуск компонентов начат")

	// Воркер обработки очереди
	a.wg.Go(func() {
		a.worker.Run(ctx)
	})

	// Планировщик отчётов (если настроен)
	if a.scheduler != nil {
		a.wg.Go(func() {
			a.scheduler.Run(ctx)
		})
	}

	// Менеджер бэкапов
	a.wg.Go(func() {
		a.backupMgr.RunPeriodic(ctx, 6*time.Hour)
	})

	slog.Info("Запуск компонентов завершён")
}

// Shutdown корректно завершает работу всех компонентов в правильном порядке
func (a *App) Shutdown() {
	slog.Info("Остановка компонентов начата")

	// 1. Ждём завершения всех горутин (они получат ctx.Done() и выйдут)
	slog.Info("Ожидание завершения фоновых задач")
	a.wg.Wait()
	slog.Info("Все фоновые задачи завершены")

	// 2. Закрываем пул Telegram-ботов (сохраняем сессии)
	if a.tgPool != nil {
		slog.Info("Закрытие Telegram-клиентов")
		a.tgPool.Close()
	}

	// 3. Закрываем соединение с БД
	if a.sqlDB != nil {
		slog.Info("Закрытие соединения с БД")
		a.sqlDB.Close()
	}

	// 4. Закрываем логгер (ВСЕГДА последним!)
	if a.logCleanup != nil {
		a.logCleanup()
	}

	slog.Info("Остановка компонентов завершена")
}
