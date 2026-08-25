package telegram

import (
	"context"
	"fmt"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/tg"
)

type Client struct {
	telegramClient *telegram.Client
	logger         Logger
	appID          int
	appHash        string
}

type Logger interface {
	Info(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
}

type Config struct {
	AppID       int
	AppHash     string
	ProxyHost   string
	ProxyPort   int
	ProxySecret string
	SessionPath string
}

func NewClient(cfg Config, logger Logger) (*Client, error) {
	// Создаем хранилище сессии
	sessionStorage := &session.FileStorage{
		Path: cfg.SessionPath,
	}

	// Создаем MTProxy resolver
	proxyAddr := fmt.Sprintf("%s:%d", cfg.ProxyHost, cfg.ProxyPort)
	resolver, err := dcs.MTProxy(proxyAddr, []byte(cfg.ProxySecret), dcs.MTProxyOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create MTProxy resolver: %w", err)
	}

	opts := telegram.Options{
		Resolver:       resolver,
		SessionStorage: sessionStorage,
		DialTimeout:    10 * time.Second,
		RetryInterval:  5 * time.Second,
		MaxRetries:     5,
	}

	client := telegram.NewClient(cfg.AppID, cfg.AppHash, opts)

	return &Client{
		telegramClient: client,
		logger:         logger,
		appID:          cfg.AppID,
		appHash:        cfg.AppHash,
	}, nil
}

func (c *Client) Run(ctx context.Context, f func(ctx context.Context, client *tg.Client) error) error {
	return c.telegramClient.Run(ctx, f)
}

func (c *Client) SendMessage(ctx context.Context, userID int64, message string) error {
	err := c.telegramClient.Run(ctx, func(ctx context.Context, api *tg.Client) error {
		_, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
			Peer:    &tg.InputPeerUser{UserID: userID},
			Message: message,
		})
		if err != nil {
			return fmt.Errorf("failed to send message to user %d: %w", userID, err)
		}
		return nil
	})

	if err != nil {
		return err
	}

	c.logger.Info("Message sent successfully", "user_id", userID)
	return nil
}
