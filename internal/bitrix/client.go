package bitrix

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Client — HTTP-клиент для отправки сообщений в Битрикс24 через вебхук
type Client struct {
	webhookURL string
	chatID     string
	httpClient *http.Client
}

// NewClient создает клиент для работы с Битрикс24
// webhookURL — URL входящего вебхука (например, https://your-domain.bitrix24.ru/rest/1/abc123xyz/)
// chatID — ID чата для отправки (например, chat123 или 123 для пользователя)
func NewClient(webhookURL, chatID string) *Client {
	return &Client{
		webhookURL: webhookURL,
		chatID:     chatID,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SendMessage отправляет сообщение в указанный чат Битрикс24
func (c *Client) SendMessage(ctx context.Context, message string) error {
	// Формируем URL метода
	methodURL := fmt.Sprintf("%s/im.message.add", c.webhookURL)

	// Подготавливаем данные
	data := url.Values{}
	data.Set("DIALOG_ID", c.chatID)
	data.Set("MESSAGE", message)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, methodURL,
		nil) // тело запроса
	if err != nil {
		return fmt.Errorf("ошибка создания запроса: %w", err)
	}
	req.URL.RawQuery = data.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка HTTP-запроса к Битрикс: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Битрикс вернул HTTP статус %d", resp.StatusCode)
	}

	slog.Info("Сообщение в Битрикс отправлено",
		"size", len(message),
	)

	return nil
}
