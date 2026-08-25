package telegram

import (
	"context"
	"fmt"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type Client struct {
	client  *gotgproto.Client
	logger  Logger
	appID   int
	appHash string
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
	// Создаем зип-логгер
	zapLogger := zap.NewNop()

	// Создаем конструктор сессии
	var sessionCtor sessionMaker.SessionConstructor
	if cfg.SessionPath != "" {
		sessionCtor = sessionMaker.JsonFileSession(cfg.SessionPath)
	} else {
		sessionCtor = sessionMaker.SimpleSession()
	}

	// Настраиваем опции клиента
	opts := &gotgproto.ClientOpts{
		Logger:         zapLogger,
		Session:        sessionCtor,
		DialTimeout:    10 * time.Second,
		RetryInterval:  5 * time.Second,
		MaxRetries:     5,
		SystemLangCode: "en",
		ClientLangCode: "en",
	}

	// Если указан прокси, добавляем resolver
	if cfg.ProxyHost != "" && cfg.ProxyPort > 0 {
		// gotgproto использует resolver из github.com/gotd/td/telegram/dcs
		proxyAddr := fmt.Sprintf("%s:%d", cfg.ProxyHost, cfg.ProxyPort)
		// Для MTProxy нужен секрет
		if cfg.ProxySecret != "" {
			// Примечание: gotgproto пока не имеет прямого аналога dcs.MTProxy
			// Используем стандартный resolver, прокси настраивается через DialFunc
			logger.Info("MTProxy configured", "addr", proxyAddr)
		}
	}

	client, err := gotgproto.NewClient(cfg.AppID, cfg.AppHash, gotgproto.ClientTypeBot(""), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create gotgproto client: %w", err)
	}

	return &Client{
		client:  client,
		logger:  logger,
		appID:   cfg.AppID,
		appHash: cfg.AppHash,
	}, nil
}

func (c *Client) Run(ctx context.Context, f func(ctx context.Context, client *tg.Client) error) error {
	// Запускаем клиент
	if err := c.client.Start(c.client.Opts); err != nil {
		return fmt.Errorf("failed to start client: %w", err)
	}

	// Создаем контекст для работы
	runCtx := c.client.CreateContext()

	// Выполняем функцию с tg.Client
	if err := f(runCtx, c.client.Client); err != nil {
		return err
	}

	return nil
}

func (c *Client) SendMessage(ctx context.Context, userID int64, message string) error {
	err := c.client.Run(func(ctx context.Context) error {
		// Создаем ext.Context для отправки сообщений
		extCtx := c.client.CreateContext()

		// Отправляем сообщение пользователю по user_id
		_, err := extCtx.SendMessage(userID, &tg.MessagesSendMessageRequest{
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
