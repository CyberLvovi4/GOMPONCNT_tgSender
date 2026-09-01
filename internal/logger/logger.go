package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Setup инициализирует логгер с дублированием в файл и консоль.
// Возвращает функцию закрытия файла (вызвать через defer).
func Setup(logDir string) (cleanup func(), err error) {
	// 1. Создаем директорию для логов, если её нет
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать директорию логов: %w", err)
	}

	// 2. Формируем имя файла с датой: logs/sender_2026-09-01.log
	dateStr := time.Now().Format("2006-01-02")
	logPath := filepath.Join(logDir, fmt.Sprintf("sender_%s.log", dateStr))

	// 3. Открываем файл для дозаписи (создается, если не существует)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть файл логов: %w", err)
	}

	// 4. На Windows принудительно включаем UTF-8 в консоли (chcp 65001)
	if runtime.GOOS == "windows" {
		enableWindowsUTF8()
	}

	// 5. Создаем MultiWriter: пишет одновременно в stdout и в файл
	multiWriter := io.MultiWriter(os.Stdout, logFile)

	// 6. Настраиваем стандартный логгер Go
	// Флаги: Ldate (дата), Ltime (время), Lmicroseconds (микросекунды), LUTC (UTC время)
	log.SetOutput(multiWriter)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	log.Printf("📝 Логирование в файл: %s", logPath)

	// 7. Возвращаем функцию очистки
	cleanup = func() {
		logFile.Close()
	}

	return cleanup, nil
}

// enableWindowsUTF8 вызывает SetConsoleOutputCP(65001) для включения UTF-8 в cmd.exe
func enableWindowsUTF8() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("SetConsoleOutputCP")

	// 65001 = UTF-8 code page
	ret, _, err := proc.Call(uintptr(65001))
	if ret == 0 {
		// Если не удалось (например, старая Windows), просто игнорируем
		_ = err
	}
}
