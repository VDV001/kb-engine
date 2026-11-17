package main

import (
	"strings"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
	"github.com/daniil/kb-engine/internal/domain"
)

// hintView is the model advice for the question being typed (#678): правилами,
// без вызова модели; роль пуста — совета нет.
type hintView struct {
	Role   string `json:"role"`
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

// hintReasons — слова подсказки; домен отдает причину кодом.
var hintReasons = map[domain.HintReason]string{
	domain.ReasonShort:    "короткий простой вопрос — хватит быстрой модели",
	domain.ReasonLong:     "длинный запрос — лучше основная модель",
	domain.ReasonCode:     "в вопросе код — лучше основная модель",
	domain.ReasonAnalysis: "анализ или сравнение — лучше основная модель",
	domain.ReasonFiles:    "приложены файлы — лучше основная модель",
}

// SetSecondModel sets the second, secondary chat model (#678); пусто — одна модель.
func (d *chatDesk) SetSecondModel(model string) error {
	d.phoneMu.Lock() // чтение-запись настроек: не теряем соседнее переключение
	defer d.phoneMu.Unlock()
	c := d.config()
	c.SecondModel = strings.TrimSpace(model)
	return d.SetSettings(c, "")
}

// Hint advises which model fits the question: главная и вторая — настройки чата;
// второй нет или она та же — выбирать не из чего, совета нет.
func (d *chatDesk) Hint(question string, files int) hintView {
	c := d.config()
	if c.SecondModel == "" || c.SecondModel == c.Model {
		return hintView{}
	}
	h := domain.SuggestModelRole(question, files)
	if h.Role == "" {
		return hintView{}
	}
	model := c.Model
	if h.Role == domain.RoleSecond {
		model = c.SecondModel
	}
	return hintView{Role: string(h.Role), Model: model, Reason: hintReasons[h.Reason]}
}

// roleModel is the chat settings of a window turn by role: "second" и вторая
// модель задана — на ней, иначе на главной (#678). Ход телефона роли не имеет.
func roleModel(c desktopconfig.Chat, role string) desktopconfig.Chat {
	if role == string(domain.RoleSecond) && c.SecondModel != "" {
		c.Model = c.SecondModel
	}
	return c
}
