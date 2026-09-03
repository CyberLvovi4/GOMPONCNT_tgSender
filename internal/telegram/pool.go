package telegram

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/glebarez/sqlite"
	"github.com/go-faster/errors"
	"github.com/gotd/td/telegram/dcs"
	"github.com/joho/godotenv"
)

type BotPool struct {
	clients   map[string]*gotgproto.Client
	receivers map[string]int64 // Name -> chat_id (справочник получателей)
	mu        sync.RWMutex

	appID      int
	appHash    string
	resolver   dcs.Resolver
	sessionDir string
}

// 🆕 Новая структура под ваш формат JSON
type configJSON struct {
	GeneralSettings struct {
		SendBy1C   bool `json:"Отправлять_средствами_1с"`
		SaveForExt bool `json:"Сохранять_для_внешнего_отправителя"`
	} `json:"GeneralSettings"`

	BotList []struct {
		Name  string `json:"Name"`
		Token string `json:"Token"`
	} `json:"BotList"`

	RecieverList []struct {
		Name   string `json:"Name"`
		ChatID int64  `json:"chat_id"`
	} `json:"RecieverList"`
}

func NewBotPool(ctx context.Context, configPath string) (*BotPool, error) {
	// Читаем .env (если он есть в CWD)
	_ = godotenv.Load()

	appIDStr := os.Getenv("APP_ID")
	appHash := os.Getenv("APP_HASH")

	if appIDStr == "" || appHash == "" {
		return nil, fmt.Errorf("отсутствуют обязательные переменные окружения (APP_ID, APP_HASH, MTPROXY_*)")
	}

	appID, err := strconv.Atoi(appIDStr)
	if err != nil {
		return nil, fmt.Errorf("неверный APP_ID: %w", err)
	}

	proxyAddr, proxySecret, err := GetProxyConfig()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения настроек прокси: %w", err)
	}

	slog.Info("Использование локального прокси",
		"address", proxyAddr,
	)

	decodedSecret, err := decodeSecret(proxySecret)
	if err != nil {
		return nil, err
	}
	resolver, err := dcs.MTProxy(proxyAddr, decodedSecret, dcs.MTProxyOptions{})
	if err != nil {
		return nil, fmt.Errorf("ошибка создания MTProxy: %w", err)
	}

	sessionDir := "./sessions"
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("не удалось создать директорию сессий: %w", err)
	}

	// Читаем JSON с токенами
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("не удалось прочитать %s: %w", configPath, err)
	}

	var cfg configJSON
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w", err)
	}

	pool := &BotPool{
		clients:    make(map[string]*gotgproto.Client),
		receivers:  make(map[string]int64),
		appID:      appID,
		appHash:    appHash,
		resolver:   resolver,
		sessionDir: sessionDir,
	}

	// Сохраняем справочник получателей
	for _, r := range cfg.RecieverList {
		pool.receivers[r.Name] = r.ChatID
		//log.Printf("📥 Загружен получатель: '%s' -> %d", r.Name, r.ChatID)
	}

	// 🆕 Итерируемся по массиву BotList
	if len(cfg.BotList) == 0 {
		return nil, fmt.Errorf("список ботов в %s пуст", configPath)
	}

	for _, bot := range cfg.BotList {
		if err := pool.addBot(ctx, bot.Name, bot.Token); err != nil {
			return nil, fmt.Errorf("ошибка инициализации бота %s: %w", bot.Name, err)
		}
		slog.Info("Бот успешно инициализирован",
			"name", bot.Name,
		)
	}

	return pool, nil
}

func (p *BotPool) addBot(ctx context.Context, botCode, token string) error {
	// Структура для передачи результата из горутины
	type result struct {
		client *gotgproto.Client
		err    error
	}

	ch := make(chan result, 1)

	// Запускаем NewClient в отдельной горутине
	go func() {
		client, err := gotgproto.NewClient(
			p.appID,
			p.appHash,
			gotgproto.ClientTypeBot(token),
			&gotgproto.ClientOpts{
				Session:          sessionMaker.SqlSession(sqlite.Open(fmt.Sprintf("./sessions/%s.db", botCode))),
				Resolver:         p.resolver,
				DisableCopyright: true,
			},
		)
		ch <- result{client, err}
	}()

	// Ждём либо результата, либо отмены через контекст
	select {
	case <-ctx.Done():
		return fmt.Errorf("инициализация бота %s отменена: %w", botCode, ctx.Err())
	case res := <-ch:
		if res.err != nil {
			return fmt.Errorf("ошибка создания клиента %s: %w", botCode, res.err)
		}

		p.mu.Lock()
		p.clients[botCode] = res.client
		p.mu.Unlock()

		return nil
	}
}

func (p *BotPool) GetClient(botCode string) (*gotgproto.Client, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	client, ok := p.clients[botCode]
	return client, ok
}

// 🆕 Метод для получения chat_id по имени получателя (пригодится позже)
func (p *BotPool) GetReceiverID(name string) (int64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	id, ok := p.receivers[name]
	return id, ok
}

func (p *BotPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for botCode, client := range p.clients {
		slog.Info("Остановка бота",
			"name", botCode,
		)
		// Вызываем встроенный метод gotgproto для корректного отключения
		client.Stop()
	}
	slog.Info("Все боты остановлены, сессии сохранены.")
}

// decodeSecret decodes an MTProxy secret that can be either hex-encoded (the
// most common share format) or base64url-encoded (as found in tg://proxy links).
func decodeSecret(s string) ([]byte, error) {
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.URLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, errors.Errorf("Невозможно декодировать secret %q как hex или base64url", s)
}
