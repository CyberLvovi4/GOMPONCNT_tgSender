package scheduler

import (
	"context"
	"log/slog"
	"time"

	"telegram-worker/internal/stats"
)

// Scheduler управляет расписанием отчётов
type Scheduler struct {
	reporter *stats.Reporter
}

// NewScheduler создает планировщик
func NewScheduler(reporter *stats.Reporter) *Scheduler {
	return &Scheduler{reporter: reporter}
}

// Run запускает циклическую проверку расписания
func (s *Scheduler) Run(ctx context.Context) {
	slog.Info("Планировщик отчётов запущен")

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	var lastReportHour int = -1

	for {
		select {
		case <-ctx.Done():
			slog.Info("Планировщик отчётов остановлен")
			return
		case <-ticker.C:
			now := time.Now()
			hour := now.Hour()
			minute := now.Minute()

			// Проверяем, не наступило ли время отчёта (ровный час, с 9 до 18)
			if minute == 0 && hour >= 9 && hour <= 18 && hour != lastReportHour {
				lastReportHour = hour

				var reportType stats.ReportType
				var from, to time.Time

				switch hour {
				case 9:
					// Ночной отчёт: с 18:00 вчера до 9:00 сегодня
					reportType = stats.ReportNight
					yesterday := now.AddDate(0, 0, -1)
					from = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 18, 0, 0, 0, now.Location())
					to = time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
				case 18:
					// Дневной отчёт: с 9:00 до 18:00 сегодня
					reportType = stats.ReportDay
					from = time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, now.Location())
					to = time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 0, 0, now.Location())
				default:
					// Часовой отчёт: за прошедший час
					reportType = stats.ReportHour
					to = now.Truncate(time.Hour)
					from = to.Add(-1 * time.Hour)
				}

				slog.Info("Запуск отчёта",
					"type", reportType,
					"periodFrom", from.Format("15:04"),
					"periodTo", to.Format("15:04"),
				)

				if err := s.reporter.SendReport(ctx, reportType, from, to); err != nil {
					slog.Error("Ошибка формирования отчёта",
						"err", err,
					)
				}
			}
		}
	}
}
