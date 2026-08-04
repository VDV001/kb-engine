// Package freshness решает, отстала ли страница-документ от базы вокруг неё.
//
// Повод: страница «что в работе сейчас» тухнет тихо. Текст остаётся
// правдоподобным — в шапке стоит «четыре PR смержены», и выглядит это ровно так
// же, как если бы их было четыре. Владелец увидел собственную страницу с этой
// строкой в тот момент, когда PR было восемь.
//
// Признак протухания здесь — НЕ возраст файла. Страница, которую не трогали
// месяц, но и база вокруг которой не менялась, верна, и мигать ей незачем:
// предупреждение, которое горит всегда, перестают читать. Отставание — это
// события, случившиеся ПОСЛЕ последней правки.
package freshness

import (
	"fmt"
	"strings"
	"time"
)

// EntryFact — то немногое, что нужно знать о записи каталога, чтобы понять,
// появилась ли она после правки страницы.
type EntryFact struct {
	ID    int
	Title string
	Added time.Time
}

// Input — состояние мира на момент вопроса.
type Input struct {
	Now time.Time
	// EditedAt — когда последний раз правили сам документ. Нулевое значение
	// означает «не знаю»: файла нет, mtime не прочитан.
	EditedAt    time.Time
	Entries     []EntryFact
	Version     string
	VersionDate time.Time
	// Operations — даты финансовых операций. Даты, а не суммы: страница
	// сообщает, что записи были, и не выносит наружу, на сколько именно.
	Operations []time.Time
}

// Fact — одна причина считать страницу отставшей, в терминах её источника.
type Fact struct {
	// Kind — какой источник ушёл вперёд: catalog, version, ledger.
	Kind  string
	Text  string
	Count int
	// IDs — несколько записей поимённо, чтобы было с чего начать. Не все:
	// двадцать заголовков подряд — вторая лента поверх той, что уже есть.
	IDs []int
}

// Report — ответ: отстала ли страница, чем именно и что можно дописать.
type Report struct {
	Behind bool
	// Unknown — дату правки узнать не удалось. Это «не знаю», а не «всё
	// хорошо»: молча промолчав, страница выглядела бы проверенной.
	Unknown  bool
	EditedAt time.Time
	Facts    []Fact
	// Draft — заготовка блока для вставки в документ. Именно заготовка:
	// движок собирает факты, слова остаются за человеком, поэтому это markdown
	// для копирования, а не файл, который движок правит сам. Автотекст в личном
	// документе за неделю превратился бы в шум, который никто не читает.
	Draft string
}

// namedEntries — сколько записей называется поимённо.
const namedEntries = 5

// Check сравнивает дату последней правки документа с тем, что произошло после.
func Check(in Input) Report {
	if in.EditedAt.IsZero() {
		return Report{Unknown: true}
	}
	out := Report{EditedAt: in.EditedAt}

	var added []EntryFact
	for _, e := range in.Entries {
		if e.Added.After(in.EditedAt) {
			added = append(added, e)
		}
	}
	if len(added) > 0 {
		f := Fact{Kind: "catalog", Count: len(added)}
		for _, e := range added[:min(namedEntries, len(added))] {
			f.IDs = append(f.IDs, e.ID)
		}
		f.Text = fmt.Sprintf("в каталоге %s после правки страницы", plural(len(added), "запись", "записи", "записей"))
		out.Facts = append(out.Facts, f)
	}

	if in.Version != "" && in.VersionDate.After(in.EditedAt) {
		out.Facts = append(out.Facts, Fact{
			Kind: "version",
			Text: fmt.Sprintf("база выросла до %s (%s)", in.Version, in.VersionDate.Format("02.01")),
		})
	}

	var ops int
	for _, d := range in.Operations {
		if d.After(in.EditedAt) {
			ops++
		}
	}
	if ops > 0 {
		out.Facts = append(out.Facts, Fact{
			Kind:  "ledger",
			Count: ops,
			Text:  fmt.Sprintf("в финансах %s после правки страницы", plural(ops, "операция", "операции", "операций")),
		})
	}

	out.Behind = len(out.Facts) > 0
	if out.Behind {
		out.Draft = draft(in, added)
	}
	return out
}

// draft собирает заготовку блока: дата сегодняшним днём и факты списком.
func draft(in Input, added []EntryFact) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", in.Now.Format(time.DateOnly))

	if len(added) > 0 {
		fmt.Fprintf(&b, "- каталог: +%d\n", len(added))
		for _, e := range added[:min(namedEntries, len(added))] {
			title := strings.TrimSpace(e.Title)
			if title == "" {
				title = "без заголовка"
			}
			fmt.Fprintf(&b, "  - #%d — %s\n", e.ID, title)
		}
		if len(added) > namedEntries {
			fmt.Fprintf(&b, "  - … и ещё %d\n", len(added)-namedEntries)
		}
	}
	if in.Version != "" && in.VersionDate.After(in.EditedAt) {
		fmt.Fprintf(&b, "- версия базы: %s (%s)\n", in.Version, in.VersionDate.Format(time.DateOnly))
	}
	var ops int
	var last time.Time
	for _, d := range in.Operations {
		if d.After(in.EditedAt) {
			ops++
			if d.After(last) {
				last = d
			}
		}
	}
	if ops > 0 {
		fmt.Fprintf(&b, "- финансы: %d операций, последняя %s\n", ops, last.Format(time.DateOnly))
	}
	return b.String()
}

// plural выбирает форму русского слова по числу. Отдельная функция, потому что
// «1 запись / 2 записи / 5 записей» — это то, что человек замечает мгновенно, а
// «1 записей» читается как поломка.
func plural(n int, one, few, many string) string {
	word := many
	switch {
	case n%10 == 1 && n%100 != 11:
		word = one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 10 || n%100 >= 20):
		word = few
	}
	return fmt.Sprintf("%d %s", n, word)
}
