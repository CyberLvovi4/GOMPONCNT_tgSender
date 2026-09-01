package formatter

import (
	"regexp"
	"strings"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
)

// ParseHTMLToTelegram парсит HTML-строку из 1С и возвращает:
// 1. Очищенный текст (с расшифрованными смайлами, без тегов)
// 2. Массив сущностей (Entities), который понимает MTProto
func ParseHTMLToTelegram(htmlText string) (string, []tg.MessageEntityClass, error) {

	// нормализуем переносы строк
	htmlText = strings.ReplaceAll(htmlText, "\r\n", "\n")
	htmlText = strings.ReplaceAll(htmlText, "\r", "\n")

	// переносы строк перед </code> гнут телегу
	var codeTrailingNewlineRe = regexp.MustCompile(`(?i)\r?\n\s*</code>`)
	htmlText = codeTrailingNewlineRe.ReplaceAllString(htmlText, "</code>")

	b := &entity.Builder{}

	if err := styling.Perform(b, html.String(nil, htmlText)); err != nil {
		return "", nil, err
	}

	cleanText, entities := b.Complete()

	return cleanText, entities, nil
}
