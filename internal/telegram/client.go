package telegram

import (
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Client struct {
	bot    *tgbotapi.BotAPI
	logger Logger
}

type Logger interface {
	Info(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
}

func NewClient(token string, logger Logger) (*Client, error) {
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	logger.Info("Telegram bot authorized", "username", bot.Self.UserName)

	return &Client{
		bot:    bot,
		logger: logger,
	}, nil
}

func (c *Client) SendMessage(chatID string, message string) error {
	chatIDInt, err := strconv.ParseInt(chatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	msg := tgbotapi.NewMessage(chatIDInt, message)
	msg.ParseMode = "HTML"

	_, err = c.bot.Send(msg)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}
