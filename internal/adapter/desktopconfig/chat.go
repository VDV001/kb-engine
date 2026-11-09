package desktopconfig

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
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

// Режимы микрофона (#478, как в english-hub): пусто — нажал-нажал (второе
// нажатие отправляет), hold — удерживать кнопку, auto — слушает сам, пауза
// отправляет фразу.
const (
	MicToggle = ""
	MicHold   = "hold"
	MicAuto   = "auto"
)

// Границы размера контекста в токенах: от маленькой локальной модели до миллиона.
const (
	MinContextWindow = 1000
	MaxContextWindow = 2000000
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
	// Listen — кто слушает диктовку: id подключения распознавания речи; пусто
	// — Apple Speech на этом Маке.
	Listen string `json:"listen,omitempty"`
	// Mic — как говорить в микрофон: MicToggle, MicHold или MicAuto.
	Mic string `json:"mic,omitempty"`
	// Accent — произношение английского текста голосом: пусто — по языку
	// ответа, en-US или en-GB.
	Accent string `json:"accent,omitempty"`
	// MemoryDir — папка портрета «Что DVK помнит обо мне» (#479); пусто —
	// портрет не ведется.
	MemoryDir string `json:"memoryDir,omitempty"`
	// MemoryServer — id источника-kb-сервера, где лежит портрет; пусто — в
	// MemoryDir. Сервер главнее папки: выбран — портрет на нем.
	MemoryServer string `json:"memoryServer,omitempty"`
	// MemoryOff — не разбирать ответы на новое о человеке.
	MemoryOff bool `json:"memoryOff,omitempty"`
	// ContextWindow — размер контекста модели в токенах; 0 — не задан, и окно
	// показывает заполнение только числом. Сервер его не сообщает, поэтому
	// человек вносит сам.
	ContextWindow int `json:"contextWindow,omitempty"`
	// MemoryQuiet — новое ждет в портрете, в чате не предлагается.
	MemoryQuiet bool `json:"memoryQuiet,omitempty"`
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

// checkMemory: папка — пусто или абсолютный путь, сервер — пусто или id
// источника; оба без управляющих символов.
func (c Chat) checkMemory() error {
	if c.MemoryDir != "" && (!filepath.IsAbs(c.MemoryDir) || utf8.RuneCountInString(c.MemoryDir) > 1024 ||
		strings.IndexFunc(c.MemoryDir, unicode.IsControl) >= 0) {
		return fmt.Errorf("%w: папка портрета %q", ErrChatSettings, c.MemoryDir)
	}
	if utf8.RuneCountInString(c.MemoryServer) > 64 || strings.IndexFunc(c.MemoryServer, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: kb-сервер портрета %q", ErrChatSettings, c.MemoryServer)
	}
	return nil
}

// checkContextWindow: 0 — не задан, иначе в границах.
func (c Chat) checkContextWindow() error {
	if c.ContextWindow != 0 && (c.ContextWindow < MinContextWindow || c.ContextWindow > MaxContextWindow) {
		return fmt.Errorf("%w: контекст %d токенов, допустимо %d-%d", ErrChatSettings, c.ContextWindow, MinContextWindow, MaxContextWindow)
	}
	return nil
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
	if err := c.checkMemory(); err != nil {
		return err
	}
	if err := c.checkContextWindow(); err != nil {
		return err
	}
	switch c.Accent {
	case "", "en-US", "en-GB":
	default:
		return fmt.Errorf("%w: акцент %q", ErrChatSettings, c.Accent)
	}
	switch c.Mic {
	case MicToggle, MicHold, MicAuto:
	default:
		return fmt.Errorf("%w: микрофон %q", ErrChatSettings, c.Mic)
	}
	if utf8.RuneCountInString(c.Listen) > 64 || strings.IndexFunc(c.Listen, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: «Слушает» %q", ErrChatSettings, c.Listen)
	}
	return nil
}

// Local reports whether the model runs on this Mac: документы никуда не уходят.
func (c Chat) Local() bool {
	u, err := url.Parse(c.URL)
	return err == nil && loopback.Host(u.Hostname())
}
