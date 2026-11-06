package desktopconfig

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/daniil/kb-engine/internal/adapter/loopback"
)

// Модель чата по умолчанию (#436, решение владельца 06.11): Vercel AI Gateway
// с ключом человека; локальная модель — адрес Ollama.
const (
	DefaultChatURL   = "https://ai-gateway.vercel.sh/v1"
	DefaultChatModel = "anthropic/claude-sonnet-5.5"
)

// Режимы чата: кто ищет документы. Пусто — по модели (локальная — окно,
// облачная — модель).
const (
	ChatModeAuto    = ""
	ChatModeContext = "context"
	ChatModeTools   = "tools"
)

// ErrChatSettings — настройки чата не подходят.
var ErrChatSettings = errors.New("настройки чата не подходят")

// Chat — модель чата окна. Ключ сюда не пишется никогда: он в Связке ключей.
type Chat struct {
	URL   string `json:"url,omitempty"`
	Model string `json:"model,omitempty"`
	Mode  string `json:"mode,omitempty"`
	// Voice — голос Mac, которым читается ответ в разговоре голосом; пусто — голос системы.
	Voice string `json:"voice,omitempty"`
}

// WithDefaults fills the empty fields.
func (c Chat) WithDefaults() Chat {
	if strings.TrimSpace(c.URL) == "" {
		c.URL = DefaultChatURL
	}
	if strings.TrimSpace(c.Model) == "" {
		c.Model = DefaultChatModel
	}
	return c
}

// Check validates the settings: адрес — то же правило, что у kb-сервера
// (https или http этого Мака), режим — из известных.
func (c Chat) Check() error {
	if err := CheckServerURL(c.URL); err != nil {
		return fmt.Errorf("%w: %w", ErrChatSettings, err)
	}
	switch c.Mode {
	case ChatModeAuto, ChatModeContext, ChatModeTools:
	default:
		return fmt.Errorf("%w: режим %q", ErrChatSettings, c.Mode)
	}
	// Голос уходит в say аргументом -v: без управляющих символов и не похожий на флаг.
	if utf8.RuneCountInString(c.Voice) > 64 || strings.HasPrefix(c.Voice, "-") ||
		strings.IndexFunc(c.Voice, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: голос %q", ErrChatSettings, c.Voice)
	}
	return nil
}

// Local reports whether the model runs on this Mac: документы никуда не уходят.
func (c Chat) Local() bool {
	u, err := url.Parse(c.URL)
	return err == nil && loopback.Host(u.Hostname())
}
