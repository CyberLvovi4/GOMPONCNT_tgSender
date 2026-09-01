package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// TgWsProxyConfig отражает структуру config.json вашего tg-ws-proxy.
// ⚠️ ВАЖНО: Проверьте ваш реальный config.json. Если поля называются иначе
// (например, "proxy_secret" вместо "secret"), просто поправьте теги `json:"..."` ниже.
type TgWsProxyConfig struct {
	Host   string `json:"host"`   // Например: "127.0.0.1"
	Port   int    `json:"port"`   // Например: 8080
	Secret string `json:"secret"` // Секрет прокси (строка)
}

// GetProxyConfig пытается получить настройки прокси.
// Приоритет: 1. Переменные окружения (.env), 2. Чтение из AppData\Roaming\TgWsProxy\config.json
func GetProxyConfig() (addr, secret string, err error) {
	// 1. Сначала проверяем .env (если вы захотите переопределить настройки вручную)
	if envAddr := os.Getenv("MTPROXY_ADDR"); envAddr != "" {
		return envAddr, os.Getenv("MTPROXY_SECRET"), nil
	}

	// 2. Автоматически находим папку AppData\Roaming текущего пользователя
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("не удалось определить системную папку конфигурации: %w", err)
	}

	// Формируем полный путь: C:\Users\Имя\AppData\Roaming\TgWsProxy\config.json
	configPath := filepath.Join(configDir, "TgWsProxy", "config.json")

	// 3. Читаем файл
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", fmt.Errorf("не удалось прочитать конфиг прокси по пути %s: %w\n(Проверьте, запущен ли tg-ws-proxy и создан ли конфиг)", configPath, err)
	}

	// 4. Парсим JSON
	var cfg TgWsProxyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", "", fmt.Errorf("ошибка парсинга JSON конфига прокси: %w", err)
	}

	// 5. Извлекаем значения с fallback'ами
	addr = cfg.Host
	if addr == "" {
		addr = "127.0.0.1" // Значение по умолчанию, если в конфиге пусто
	}
	port := cfg.Port
	if port == 0 {
		port = 1443 // Значение по умолчанию, если в конфиге пусто
	}

	secret = cfg.Secret
	if secret == "" {
		return "", "", fmt.Errorf("секрет прокси не найден в конфиге %s", configPath)
	}

	return fmt.Sprintf("%v:%v", addr, port), secret, nil
}
