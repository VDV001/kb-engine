package freshness_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/freshness"
)

// Страницы Team и Projects тухнут так же, как Now, но смотрят на них реже —
// значит врут дольше. Проверять их одинаково нельзя: опоры у каждой свои, а у
// какой-то их нет вовсе, и это надо говорить вслух, а не молчать.
func TestVersionMention(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		current string
		want    string // "" — находки нет
	}{
		{
			name: "версия рядом с именем продукта — находка",
			// Ровно живой случай: карточка kb-engine на странице Projects
			// говорит v0.5.0, когда движок отвечает 0.15.0.
			text:    `{"note":"v0.5.0 · AGPL-3.0 · github.com/VDV001/kb-engine"}`,
			current: "0.15.0",
			want:    "v0.5.0",
		},
		{
			name:    "версия совпала — молчим",
			text:    `{"note":"v0.15.0 · github.com/VDV001/kb-engine"}`,
			current: "0.15.0",
			want:    "",
		},
		{
			name:    "версия без имени продукта — не наша",
			text:    `{"note":"v0.113.0 · другой проект"}`,
			current: "0.15.0",
			want:    "",
		},
		{
			name:    "имя без версии — сверять нечего",
			text:    `{"note":"github.com/VDV001/kb-engine"}`,
			current: "0.15.0",
			want:    "",
		},
		{
			// Своей версии движок может не знать — сборка из исходников даёт
			// псевдоверсию. Тогда он молчит, а не объявляет всех отставшими.
			name:    "своя версия неизвестна — не судим",
			text:    `{"note":"v0.5.0 · kb-engine"}`,
			current: "",
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := freshness.VersionMention(tc.text, "kb-engine", tc.current)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("находка там, где её быть не должно: %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("отставание версии не найдено")
			}
			if !strings.Contains(got.Text, tc.want) || !strings.Contains(got.Text, tc.current) {
				t.Errorf("текст находки не называет обе версии: %q", got.Text)
			}
		})
	}
}

// Источник, у которого опор нет вовсе, обязан сказать именно это. Молча
// показанная зелёная галочка означала бы «проверено», а проверки не было.
func TestSource_saysWhenThereIsNothingToCompareWith(t *testing.T) {
	got := freshness.CheckSource(freshness.Source{
		Name: "Team", Flag: "--team",
		EditedAt: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC),
		Now:      time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
		// Anchored не выставлен: у состава отдела нет опор в базе.
	})

	if got.Behind {
		t.Error("объявил отставание, не имея с чем сравнивать")
	}
	if !got.NoAnchors {
		t.Error("не признался, что сверять не с чем")
	}
	// Дату правки при этом называет: возраст — факт, даже когда он не приговор.
	if got.EditedAt.IsZero() {
		t.Error("дата правки потеряна")
	}
}

// Источник с опорами ведёт себя как Now: называет, что случилось после правки.
func TestSource_carriesTheFactsItWasGiven(t *testing.T) {
	got := freshness.CheckSource(freshness.Source{
		Name: "Projects", Flag: "--projects", Anchored: true,
		EditedAt: time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC),
		Now:      time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
		Facts: []freshness.Fact{
			{Kind: "version", Text: "страница называет движок v0.5.0, сейчас 0.15.0"},
		},
	})

	if !got.Behind {
		t.Fatal("отставание не объявлено при наличии находки")
	}
	if got.NoAnchors {
		t.Error("сказал «сверять не с чем», имея находку")
	}
}

// «Опор нет» и «опоры есть, но всё сошлось» — разные вещи, и путать их нельзя.
// Поймано живым прогоном: страница Now, у которой опор три, показывалась как
// «сверять не с чем» ровно потому, что в тот день ничего не разошлось.
func TestSource_distinguishesNothingToCompareFromNothingFound(t *testing.T) {
	fresh := freshness.CheckSource(freshness.Source{
		Name: "Now", Flag: "--now", Anchored: true,
		EditedAt: time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
		Now:      time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
	})
	if fresh.NoAnchors {
		t.Error("источник с опорами назван «сверять не с чем» только потому, что находок нет")
	}
	if fresh.Behind {
		t.Error("отставание объявлено без единой находки")
	}
}

// Псевдоверсия целиком на экран не уезжает: «сейчас v0.15.1-0.2026…+dirty» не
// отвечает на вопрос, какая версия сейчас.
//
// Первая редакция этого требования звучала как «с псевдоверсией не сравнивать
// вовсе», и она была неверной: `kbup` собирает движок именно так, то есть
// проверка не срабатывала бы никогда в основном сценарии. Сравнивать надо, но с
// базовым выпуском, а не со строкой сборки.
func TestVersionMention_showsTheBaseReleaseNotTheBuildString(t *testing.T) {
	got := freshness.VersionMention(`{"note":"v0.5.0 · kb-engine"}`, "kb-engine",
		"v0.15.1-0.20260804145247-fea441f0ae51+dirty")
	if got == nil {
		t.Fatal("отставание не найдено на сборке из исходников")
	}
	for _, bad := range []string{"dirty", "20260804", "fea441f"} {
		if strings.Contains(got.Text, bad) {
			t.Errorf("на экран уехала строка сборки (%q): %q", bad, got.Text)
		}
	}
}

// Сборка из исходников — основной способ, которым владелец обновляет движок
// (`kbup`), и версия у неё псевдо: 0.15.1-0.20260804151919-e9b33a0aa068. Пока
// такая версия считалась «не знаю», проверка Projects не срабатывала никогда
// именно в том сценарии, ради которого заводилась.
//
// Но псевдоверсия не пустая: Go строит её как «следующий patch после последнего
// тега», то есть 0.15.1-0.… означает «собрано после 0.15.0».
func TestBaseRelease(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.15.0", "0.15.0"},                                     // чистый выпуск
		{"v0.15.0", "0.15.0"},                                    // с префиксом
		{"0.15.1-0.20260804151919-e9b33a0aa068", "0.15.0"},       // собрано после 0.15.0
		{"0.15.1-0.20260804151919-e9b33a0aa068+dirty", "0.15.0"}, // с незакоммиченным
		// patch=0 при суффиксе «-0.» в жизни не встречается: Go строит
		// псевдоверсию как patch+1 от найденного тега. Нуль означает, что перед
		// нами не та форма, о которой мы думаем, — и мы не гадаем.
		{"0.1.0-0.20260101000000-abcdef123456", ""},
		{"dev", ""}, // ничего не знаем
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := freshness.BaseRelease(tc.in); got != tc.want {
				t.Errorf("BaseRelease(%q) = %q, ожидалось %q", tc.in, got, tc.want)
			}
		})
	}
}

// Версия страницы сравнивается с базовым выпуском, а не с псевдоверсией
// целиком: «сейчас 0.15.1-0.2026…+dirty» не отвечает на вопрос, какая версия
// сейчас, а «сейчас 0.15.0» отвечает.
func TestVersionMention_usesTheBaseReleaseOfAPseudoVersion(t *testing.T) {
	got := freshness.VersionMention(`{"note":"v0.5.0 · kb-engine"}`, "kb-engine",
		"0.15.1-0.20260804151919-e9b33a0aa068")
	if got == nil {
		t.Fatal("отставание не найдено при собранной из исходников версии")
	}
	if !strings.Contains(got.Text, "0.15.0") || strings.Contains(got.Text, "dirty") {
		t.Errorf("в тексте не базовый выпуск: %q", got.Text)
	}
}
