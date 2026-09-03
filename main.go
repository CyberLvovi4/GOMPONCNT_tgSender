package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"

	"telegram-worker/internal/app"
)

func main() {
	// 🛡️ 1. RECOVER — САМЫЙ ПЕРВЫЙ defer (выполнится последним)
	// Перехватит паники из любого места, включая другие defers
	defer func() {
		if r := recover(); r != nil {
			// Пытаемся залогировать панику (логгер уже может быть закрыт,
			// поэтому дублируем в stderr)
			msg := fmt.Sprintf("Критическая ошибка (panic): %v", r)
			slog.Error(msg)
			fmt.Fprintln(os.Stderr, msg)

			// Если запуск "в один клик" — ждём Enter
			if len(os.Args) == 1 {
				fmt.Println("\n[Нажмите Enter для выхода...]")
				fmt.Scanln()
			}
		}
	}()

	// 2. Загружаем переменные окружения
	if err := godotenv.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "!!! .env не загружен: %v (будут использоваться системные переменные)\n", err)
	} else {
		fmt.Println(".env загружен")
	}

	// 3. Создаём контекст с обработкой Ctrl+C
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 4. Инициализируем приложение
	application, err := app.Init(ctx)
	if err != nil {
		slog.Error("Ошибка инициализации приложения",
			"err", err,
		)
		panic(err)
	}

	// 🛡️ 5. GARANTEED SHUTDOWN — всегда выполнится, даже при панике в Run
	defer application.Shutdown()

	// 6. Запускаем все компоненты
	application.Run(ctx)

	// 7. Блокируемся до сигнала остановки
	<-ctx.Done()
	slog.Info("Получен сигнал остановки")
}
