package domain_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Тесты на ГРАНИЦЫ, найденные мутационным прогоном 23.08.2026.
//
// Это backfill покрытия, а не TDD: код уже написан и работает. Названо так
// честно, потому что цикл red-green здесь не проходился.
//
// Как нашлись. `gremlins unleash ./internal/domain` подсаживает в код по одному
// изменению («>» на «>=», «+» на «-», «!» долой) и смотрит, упадёт ли набор.
// Из 117 исполнимых мутантов 107 были убиты, а десять ВЫЖИЛИ — то есть в этих
// местах подсаженный дефект проходит мимо тестов. Все десять оказались
// граничными условиями, и одно из них про деньги.
//
// ⚠️ Инструмент из коробки дал «Timed out: 117, Test efficacy 0.00%» — то есть
// правдоподобный ноль вместо результата: таймаут считается от базового прогона
// (0,1 с), а каждому мутанту нужна пересборка пакета. Нулевой результат здесь
// означал сломанный замер, а не идеальные тесты.

// money.go:168 — переполнение при разборе суммы. Выжили ARITHMETIC_BASE и
// INVERT_NEGATIVES: проверка есть, а теста на неё не было.
func TestParseMoney_overflowBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{"наибольшая помещающаяся сумма", "92233720368547758.07", true},
		{"на копейку больше — не помещается", "92233720368547758.08", false},
		{"наименьшая отрицательная помещается", "-92233720368547758.08", true},
		{"на копейку ниже — не помещается", "-92233720368547758.09", false},
		{"рубли за пределом", "92233720368547759.00", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := domain.ParseMoney(tc.raw)
			if tc.ok && err != nil {
				t.Fatalf("%q должна разбираться, получено: %v", tc.raw, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%q не помещается в копейки, но принята как %v", tc.raw, got)
			}
			if tc.ok {
				// Значение, которое пакет умеет записать, он обязан прочитать обратно.
				back, err := domain.ParseMoney(got.String())
				if err != nil || back != got {
					t.Errorf("круг записи и чтения не сошёлся: %v → %q → %v (%v)", got, got.String(), back, err)
				}
			}
		})
	}
}

// money.go:44 — граница перевода рублей с копейками в копейки. Комментарий в
// коде прямо называет её точной, но проверял её только сам комментарий.
// MoneyFromFloat берёт РУБЛИ и умножает на сто, поэтому граница делится на сто.
func TestMoneyFromFloat_boundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float64
		ok   bool
	}{
		{"ноль", 0, true},
		{"обычная сумма", 1234.56, true},
		{"около нижнего предела", math.MinInt64 / 100, true},
		{"далеко ниже предела", math.MinInt64, false},
		{"далеко выше предела", -float64(math.MinInt64), false},
		// Ровно 2^63 копеек: строгое «меньше» обязано отвергнуть. С нестрогим
		// значение прошло бы и переполнило int64 — мутант, выживший в первом
		// прогоне, жил именно здесь.
		{"ровно 2^63 копеек — не помещается", 92233720368547758.08, false},
		{"NaN — не сумма", math.NaN(), false},
		{"бесконечность — не сумма", math.Inf(1), false},
		{"минус бесконечность — не сумма", math.Inf(-1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.MoneyFromFloat(tc.in)
			if tc.ok && err != nil {
				t.Fatalf("%v должно приниматься, получено: %v", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("%v не помещается в копейки, но принято", tc.in)
			}
		})
	}
}

// linkstatus.go:46 — код ответа вне диапазона HTTP. Выжили обе границы.
func TestClassifyLinkStatus_rangeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		code int
		ok   bool
	}{
		{99, false}, {100, true}, {599, true}, {600, false}, {0, false}, {-1, false},
	} {
		_, err := domain.ClassifyLinkStatus(tc.code)
		if tc.ok && err != nil {
			t.Errorf("код %d — настоящий статус HTTP, отвергнут: %v", tc.code, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("код %d не статус HTTP, но принят", tc.code)
		}
	}
}

// runrecord.go:49 и :52 — длительность прогона. Ноль это законная длительность,
// отрицательная — нет; тест на саму границу отсутствовал.
func TestNewRunRecord_durationBoundary(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		took time.Duration
		ok   bool
	}{
		{"нулевая длительность законна", 0, true},
		{"наносекунда законна", time.Nanosecond, true},
		{"отрицательная отвергается", -time.Nanosecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NewRunRecord("audit", nil, now, tc.took, 0, now)
			if tc.ok && err != nil {
				t.Fatalf("длительность %v законна, отвергнута: %v", tc.took, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("длительность %v отрицательная, но принята", tc.took)
			}
		})
	}
}

// entry.go:185 — номер редакции. Единица законна, ноль нет; выжил мутант,
// сдвигавший границу.
func TestNewEntry_revisionBoundary(t *testing.T) {
	for _, tc := range []struct {
		rev int
		ok  bool
	}{
		{1, true}, {2, true}, {0, false}, {-1, false},
	} {
		p := validArticle(t)
		p.Verdict = nil
		rev := tc.rev
		p.Revision = &rev
		_, err := domain.NewEntry(p)
		if tc.ok && err != nil {
			t.Errorf("редакция %d законна, отвергнута: %v", tc.rev, err)
		}
		if !tc.ok {
			if err == nil {
				t.Errorf("редакция %d недопустима, но принята", tc.rev)
			} else if !strings.Contains(err.Error(), "revision") {
				t.Errorf("причина отказа не называет редакцию: %v", err)
			}
		}
	}
}

// runrecord.go:52 — верхняя граница кода возврата. 255 законен, 256 нет;
// без этого теста нестрогое сравнение проходило мимо набора.
func TestNewRunRecord_exitCodeBoundary(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		code int
		ok   bool
	}{
		{0, true}, {1, true}, {255, true}, {256, false}, {-1, false},
	} {
		_, err := domain.NewRunRecord("audit", nil, now, time.Second, tc.code, now)
		if tc.ok && err != nil {
			t.Errorf("код возврата %d законен, отвергнут: %v", tc.code, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("код возврата %d вне диапазона, но принят", tc.code)
		}
	}
}
