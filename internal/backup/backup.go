package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Manager управляет резервным копированием БД
type Manager struct {
	db          *sql.DB
	backupDir   string
	dbFileName  string // например, "messages.db"
	retainCount int    // сколько последних бэкапов хранить
	retainDays  int    // максимум дней хранения (0 = не ограничено)
}

// NewManager создает менеджер бэкапов
func NewManager(db *sql.DB, backupDir, dbFileName string, retainCount, retainDays int) (*Manager, error) {
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать директорию бэкапов: %w", err)
	}

	return &Manager{
		db:          db,
		backupDir:   backupDir,
		dbFileName:  dbFileName,
		retainCount: retainCount,
		retainDays:  retainDays,
	}, nil
}

// MakeBackup выполняет атомарное резервное копирование БД.
// Безопасно работает при активных записях (использует VACUUM INTO).
func (m *Manager) MakeBackup() error {
	start := time.Now()

	// Формируем имя файла бэкапа: messages_2026-09-01_14-30-00.db
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	baseName := strings.TrimSuffix(m.dbFileName, filepath.Ext(m.dbFileName))
	backupFileName := fmt.Sprintf("%s_%s.db", baseName, timestamp)
	backupPath := filepath.Join(m.backupDir, backupFileName)

	// Временный файл для атомарной записи (чтобы не повредить существующий бэкап при сбое)
	tmpPath := backupPath + ".tmp"

	// Удаляем временный файл, если он остался от предыдущего неудачного бэкапа
	_ = os.Remove(tmpPath)

	// 🔥 МАГИЯ SQLite: VACUUM INTO делает атомарную "горячую" копию БД
	// Это безопаснее, чем Backup API, и не требует type assertion к драйверу
	query := fmt.Sprintf("VACUUM INTO '%s'", escapeSQLitePath(tmpPath))

	if _, err := m.db.Exec(query); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ошибка выполнения VACUUM INTO: %w", err)
	}

	// Переименовываем временный файл в итоговый (атомарная операция на Windows)
	if err := os.Rename(tmpPath, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("ошибка переименования бэкапа: %w", err)
	}

	// Получаем размер файла для лога
	info, _ := os.Stat(backupPath)
	sizeMB := float64(info.Size()) / 1024 / 1024

	duration := time.Since(start)
	log.Printf("💾 Бэкап создан: %s (%.2f МБ, за %v)", backupFileName, sizeMB, duration.Round(time.Millisecond))

	// Удаляем старые бэкапы согласно политике хранения
	if err := m.cleanup(); err != nil {
		log.Printf("⚠️ Ошибка при очистке старых бэкапов: %v", err)
	}

	return nil
}

// escapeSQLitePath экранирует одинарные кавычки в пути (для VACUUM INTO)
func escapeSQLitePath(path string) string {
	return strings.ReplaceAll(path, "'", "''")
}

// cleanup удаляет старые бэкапы согласно политике хранения
func (m *Manager) cleanup() error {
	// Находим все файлы бэкапов
	pattern := filepath.Join(m.backupDir, "*.db")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	// Фильтруем только файлы, соответствующие шаблону нашего бэкапа
	baseName := strings.TrimSuffix(m.dbFileName, filepath.Ext(m.dbFileName))
	var backupFiles []string
	for _, f := range files {
		name := filepath.Base(f)
		if strings.HasPrefix(name, baseName+"_") && name != m.dbFileName {
			backupFiles = append(backupFiles, f)
		}
	}

	if len(backupFiles) == 0 {
		return nil
	}

	// Сортируем по времени модификации (новые в конце)
	sort.Slice(backupFiles, func(i, j int) bool {
		infoI, _ := os.Stat(backupFiles[i])
		infoJ, _ := os.Stat(backupFiles[j])
		return infoI.ModTime().Before(infoJ.ModTime())
	})

	now := time.Now()
	deleted := 0

	// 1. Удаляем по количеству (оставляем только retainCount последних)
	if m.retainCount > 0 && len(backupFiles) > m.retainCount {
		toDelete := backupFiles[:len(backupFiles)-m.retainCount]
		for _, f := range toDelete {
			if err := os.Remove(f); err == nil {
				deleted++
				log.Printf("🗑️ Удален старый бэкап (по количеству): %s", filepath.Base(f))
			}
		}
		// Обновляем список после удаления
		backupFiles = backupFiles[len(backupFiles)-m.retainCount:]
	}

	// 2. Удаляем по возрасту (если старше retainDays)
	if m.retainDays > 0 {
		cutoff := now.AddDate(0, 0, -m.retainDays)
		for _, f := range backupFiles {
			info, err := os.Stat(f)
			if err != nil {
				continue
			}
			if info.ModTime().Before(cutoff) {
				if err := os.Remove(f); err == nil {
					deleted++
					log.Printf("🗑️ Удален старый бэкап (по возрасту): %s", filepath.Base(f))
				}
			}
		}
	}

	if deleted > 0 {
		log.Printf("🧹 Очистка завершена: удалено %d старых бэкапов", deleted)
	}

	return nil
}

// RunPeriodic запускает периодическое резервное копирование.
// Блокирует вызывающую горутину, поэтому запускайте через go.
func (m *Manager) RunPeriodic(ctx context.Context, interval time.Duration) {
	// Делаем первый бэкап сразу при старте
	if err := m.MakeBackup(); err != nil {
		log.Printf("❌ Ошибка при стартовом бэкапе: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("⏹️ Периодический бэкап остановлен")
			return
		case <-ticker.C:
			if err := m.MakeBackup(); err != nil {
				log.Printf("❌ Ошибка при периодическом бэкапе: %v", err)
			}
		}
	}
}
