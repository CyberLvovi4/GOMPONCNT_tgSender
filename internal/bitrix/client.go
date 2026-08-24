package bitrix

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	webhookURL string
	logger     Logger
	httpClient *http.Client
}

type Logger interface {
	Info(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
}

type BitrixMessage struct {
	Message string `json:"message"`
}

func NewClient(webhookURL string, logger Logger) *Client {
	return &Client{
		webhookURL: webhookURL,
		logger:     logger,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) SendLog(logText string) error {
	payload := BitrixMessage{
		Message: logText,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequest("POST", c.webhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bitrix returned status: %d", resp.StatusCode)
	}

	return nil
}
