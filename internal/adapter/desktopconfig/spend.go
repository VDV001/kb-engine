package desktopconfig

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// ErrSpend — лимит или своя цена модели в «Деньгах» заданы неверно.
var ErrSpend = errors.New("настройки «Денег» заданы неверно")

// Spend — экран «Деньги» статистики (#493): лимит месяца и свои цены моделей.
// Цены шлюза здесь не лежат: они в кэше рядом с журналом вызовов.
type Spend struct {
	// Limit — долларов в месяц, как ввел человек; пусто — без лимита.
	Limit string `json:"limit,omitempty"`
	// Prices — своя цена модели, долларов за 1 млн токенов; главнее цены шлюза.
	Prices map[string]Price `json:"prices,omitempty"`
}

// Price is a model's own price per million input and output tokens.
type Price struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

// CheckLimit validates a monthly limit: пусто или неотрицательное число долларов.
func CheckLimit(s string) error {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64)
	if err != nil || v < 0 || v > 1e9 {
		return fmt.Errorf("%w: лимит %q — число долларов от 0", ErrSpend, s)
	}
	return nil
}

// Check validates the spend settings the window sends.
func (s Spend) Check() error {
	if err := CheckLimit(s.Limit); err != nil {
		return err
	}
	for m, p := range s.Prices {
		if strings.TrimSpace(m) == "" {
			return fmt.Errorf("%w: цена без модели", ErrSpend)
		}
		if _, err := domain.ParseModelPricePerMillion(p.In, p.Out); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrSpend, m, err)
		}
	}
	return nil
}

// dropBadSpend чинит «Деньги» при чтении, как dropBadAccents: испорченный
// лимит снимается, испорченная цена отбрасывается — окно открывается.
func (c *Config) dropBadSpend() {
	if CheckLimit(c.Spend.Limit) != nil {
		c.Spend.Limit = ""
	}
	maps.DeleteFunc(c.Spend.Prices, func(m string, p Price) bool {
		_, err := domain.ParseModelPricePerMillion(p.In, p.Out)
		return strings.TrimSpace(m) == "" || err != nil
	})
}
