package domain_test

import (
	"errors"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
)

// Цена модели — долларов за токен строкой, как отдает шлюз, или за 1 млн
// токенов, как вводит человек; внутри — пикодоллары, без плавающей точки:
// сумма месяца не накапливает ошибку округления.
func TestModelPrice(t *testing.T) {
	for _, tc := range []struct {
		name          string
		perToken      bool
		in, out       string
		tokIn, tokOut int
		want          int64 // пикодолларов
	}{
		{"шлюз: Haiku 5.5, по миллиону", true, "0.0000001", "0.0000005", 1_000_000, 1_000_000, 600_000_000_000},
		{"человек: те же 0,10 и 0,50 за миллион", false, "0.10", "0.50", 1_000_000, 1_000_000, 600_000_000_000},
		{"запятая вместо точки", false, "0,1", "0,5", 1_000_000, 1_000_000, 600_000_000_000},
		{"один вызов: 5200 на вход, 640 на выход по Sonnet", true, "0.000002", "0.00001", 5200, 640, 16_800_000_000},
		{"бесплатная локальная модель", false, "0", "0", 5200, 640, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parse := domain.ParseModelPricePerMillion
			if tc.perToken {
				parse = domain.ParseModelPricePerToken
			}
			p, err := parse(tc.in, tc.out)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.Cost(tc.tokIn, tc.tokOut); got != tc.want {
				t.Fatalf("Cost = %d, want %d", got, tc.want)
			}
		})
	}
}

// Отрицательная, пустая, не число и точнее пикодоллара за токен — отказ, а не
// тихий ноль: ноль в «Деньгах» читался бы как «бесплатно».
func TestModelPrice_rejects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		perToken bool
		in, out  string
	}{
		{"пусто", false, "", "0.5"},
		{"минус", false, "-0.1", "0.5"},
		{"не число", false, "0.1", "дешево"},
		{"точнее пикодоллара", true, "0.0000000000001", "0"},
		{"два разделителя", false, "0.1.2", "0"},
		{"экспонента", true, "1e-7", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parse := domain.ParseModelPricePerMillion
			if tc.perToken {
				parse = domain.ParseModelPricePerToken
			}
			if _, err := parse(tc.in, tc.out); !errors.Is(err, domain.ErrInvalidModelPrice) {
				t.Fatalf("err = %v, want ErrInvalidModelPrice", err)
			}
		})
	}
}

// За миллион — то, что видит человек в настройках: обратно из пикодолларов.
func TestModelPrice_perMillion(t *testing.T) {
	p, err := domain.ParseModelPricePerToken("0.0000001", "0.0000005")
	if err != nil {
		t.Fatal(err)
	}
	if in, out := p.PerMillion(); in != "0.1" || out != "0.5" {
		t.Fatalf("PerMillion = %q, %q", in, out)
	}
}
