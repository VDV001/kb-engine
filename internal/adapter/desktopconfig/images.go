package desktopconfig

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Images — модель изображений, подключенная человеком (#678): адрес, имя;
// баланс он пополняет сам. Ключ сюда не пишется никогда: он в Связке ключей.
type Images struct {
	URL   string `json:"url,omitempty"`
	Model string `json:"model,omitempty"`
}

// On reports whether an image model is connected.
func (i Images) On() bool { return i.URL != "" && i.Model != "" }

// Check validates the settings: пусто целиком — не подключена; иначе адрес — то
// же правило, что у kb-сервера (https или этот Мак), имя — без управляющих символов.
func (i Images) Check() error {
	if i.URL == "" && i.Model == "" {
		return nil
	}
	if i.URL == "" || strings.TrimSpace(i.Model) == "" {
		return fmt.Errorf("%w: модель изображений — нужны и адрес, и имя модели", ErrChatSettings)
	}
	if err := CheckServerURL(i.URL); err != nil {
		return fmt.Errorf("%w: модель изображений: %w", ErrChatSettings, err)
	}
	if utf8.RuneCountInString(i.Model) > 200 || strings.IndexFunc(i.Model, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: имя модели изображений %q", ErrChatSettings, i.Model)
	}
	return nil
}
