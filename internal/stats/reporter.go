package stats

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"telegram-worker/internal/bitrix"
	"telegram-worker/internal/db"
)

// ReportType определяет тип отчёта
type ReportType int

const (
	ReportNight ReportType = iota // Ночной отчёт (18:00 — 9:00)
	ReportHour                    // Часовой отчёт
	ReportDay                     // Дневной отчёт (9:00 — 18:00)
)

// Reporter формирует и отправляет отчёты в Битрикс
type Reporter struct {
	repo   db.Repository
	bitrix *bitrix.Client
}

// NewReporter создает репортер
func NewReporter(repo db.Repository, bitrixClient *bitrix.Client) *Reporter {
	return &Reporter{
		repo:   repo,
		bitrix: bitrixClient,
	}
}

// SendReport формирует и отправляет отчёт за указанный период
func (r *Reporter) SendReport(ctx context.Context, reportType ReportType, from, to time.Time) error {
	slog.Info("Формирование отчёта за период",
		"periodFrom", from.Format("02.01 15:04"),
		"periodTo", to.Format("02.01 15:04"),
	)

	// Получаем статистику из БД
	sentCount, err := r.repo.GetSentCount(ctx, from, to)
	if err != nil {
		return fmt.Errorf("ошибка получения счетчика успешных: %w", err)
	}

	failedItems, err := r.repo.GetFailedMessages(ctx, from, to)
	if err != nil {
		return fmt.Errorf("ошибка получения списка неудачных: %w", err)
	}

	// Формируем текст отчёта
	message := r.formatReport(reportType, from, to, sentCount, failedItems)

	// Отправляем в Битрикс
	if err := r.bitrix.SendMessage(ctx, message); err != nil {
		return fmt.Errorf("ошибка отправки в Битрикс: %w", err)
	}

	slog.Info("Отчёт отправлен в Битрикс")

	return nil
}

// formatReport формирует BBCode-сообщение для Битрикс24
// Обновляем метод formatReport
func (r *Reporter) formatReport(reportType ReportType, from, to time.Time, sentCount int, failedItems []db.FailedItem) string {
	var sb strings.Builder

	// Заголовок
	switch reportType {
	case ReportNight:
		sb.WriteString("[b]📊 Статистика за ночь[/b]\n")
	case ReportDay:
		sb.WriteString("[b]📊 Итоги рабочего дня[/b]\n")
	case ReportHour:
		sb.WriteString("[b]📊 Статистика за час[/b]\n")
	}

	fmt.Fprintf(&sb, "Период: %s — %s\n\n",
		from.Format("02.01.2006 15:04"),
		to.Format("02.01.2006 15:04"))

	// Успешные
	fmt.Fprintf(&sb, "[B]✅ Успешно отправлено:[/B] %d\n\n", sentCount)

	// Неуспешные
	totalFailed := 0
	for _, item := range failedItems {
		totalFailed += item.Count
	}

	if totalFailed == 0 {
		sb.WriteString("[B]❌ Неудачных отправок нет[/B]\n")
	} else {
		fmt.Fprintf(&sb, "[B]❌ Неудачных отправок:[/B] %d\n\n", totalFailed)

		for _, item := range failedItems {
			// 🌟 Формируем человекочитаемое имя получателя
			recipientName := formatRecipientName(item.ChatUsername, item.ChatID)

			// Формат: Отправитель → Получатель
			fmt.Fprintf(&sb, "• [B]%s[/B] → [I]%s[/I]\n", item.Sender, recipientName)
			fmt.Fprintf(&sb, "  [COLOR=gray]%s[/COLOR] [COLOR=red](%d раз)[/COLOR]\n\n",
				truncateError(item.Error, 100), item.Count)
		}
	}

	return sb.String()
}

// formatRecipientName возвращает человекочитаемое имя получателя
func formatRecipientName(chatUsername *string, chatID int64) string {
	// Если имя есть и не пустое — используем его
	if chatUsername != nil && strings.TrimSpace(*chatUsername) != "" {
		return *chatUsername
	}
	// Иначе возвращаем ID
	return fmt.Sprintf("ID:%d", chatID)
}

// truncateError обрезает длинный текст ошибки
func truncateError(errText string, maxLen int) string {
	if len(errText) <= maxLen {
		return errText
	}
	return errText[:maxLen] + "..."
}
