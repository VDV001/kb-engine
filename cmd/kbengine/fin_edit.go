package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/filelock"
	"github.com/daniil/kb-engine/internal/adapter/financejsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/finance"
)

// ErrNoSuchEntry сообщает, что записи с таким идентификатором в леджере нет.
var ErrNoSuchEntry = errors.New("записи с таким id нет")

// runFinEdit правит одну существующую запись.
//
// Появилась потому, что исправить записанную трату было нечем: единственным
// способом оставалась правка файла руками, а это ровно та дверь, через которую
// в книгу однажды попала строка без id.
//
// Пустое значение флага означает «не передавали»: правка счёта не должна
// уносить заметку. Стирание выражается явно — `--account=`, и различает их
// flag.Visit, а не догадка по содержимому строки.
func runFinEdit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fin edit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ledgerPath := ledgerFlags(fs)
	id := fs.String("id", "", "id of the entry to edit")
	amount := fs.String("amount", "", "new amount")
	category := fs.String("cat", "", "new category")
	sub := fs.String("sub", "", "new subcategory (--sub= clears it)")
	place := fs.String("place", "", "new place (--place= clears it)")
	note := fs.String("note", "", "new description (--note= clears it)")
	source := fs.String("source", "", "new source")
	account := fs.String("account", "", "new account (--account= clears it)")
	date := fs.String("date", "", "new date as YYYY-MM-DD")
	if code, stop := parseFlags(fs, args); stop {
		return code
	}
	if *ledgerPath == "" {
		fmt.Fprintln(stderr, "fin edit: --ledger is required")
		return 2
	}
	if *id == "" {
		fmt.Fprintln(stderr, "fin edit: --id is required")
		return 2
	}

	p, err := editParams(fs, *amount, *date, editText{
		category: *category, sub: *sub, place: *place,
		note: *note, source: *source, account: *account,
	})
	if err != nil {
		fmt.Fprintf(stderr, "fin edit: %v\n", err)
		return 1
	}

	rec, err := editInLedger(*ledgerPath, *id, p)
	if err != nil {
		fmt.Fprintf(stderr, "fin edit: %v\n", err)
		return 1
	}
	tx := rec.Transaction()
	fmt.Fprintf(stdout, "fin edit: %s  %s  %10s  %s %s %s\n",
		tx.ID(), tx.Date().Format(time.DateOnly), tx.Amount(),
		tx.Category(), tx.Description(), tx.Account())
	return 0
}

// editInLedger — единственный путь правки, как appendChecked единственный путь
// записи. Экран терминала зовёт эту же функцию: две копии «загрузить → изменить
// → отсортировать → сохранить» разошлись бы ровно так же, как разошлись когда-то
// книга и леджер.
func editInLedger(ledgerPath, id string, p finance.EditParams) (finance.Record, error) {
	// Замок на всё чтение-правку-запись, по той же причине, что и у добавления:
	// проверка повтора судит по прочитанному, а перезаписывается файл целиком.
	var rec finance.Record
	err := filelock.With(ledgerPath, func() error {
		var err error
		rec, err = editUnderLock(ledgerPath, id, p)
		return err
	})
	return rec, err
}

func editUnderLock(ledgerPath, id string, p finance.EditParams) (finance.Record, error) {
	recs, err := financejsonl.Load(ledgerPath, time.Now)
	if err != nil {
		return finance.Record{}, err
	}
	for i, rec := range recs {
		if rec.Transaction().ID() != id {
			continue
		}
		edited, err := finance.Edit(rec, p, time.Now)
		if err != nil {
			return finance.Record{}, err
		}
		// Та же проверка, что стоит на добавлении, и по той же причине: дубль,
		// созданный правкой, через неделю неразрешим — никто не скажет, какая
		// из двух строк была настоящей покупкой. Спрашивается ДО записи и
		// отвечает тем же ErrRepeat, поэтому обе поверхности отличают «это
		// повтор» от «файл не открылся» одинаково.
		if twin := finance.RepeatOf(recs, edited); twin != nil {
			tx := twin.Transaction()
			return finance.Record{}, fmt.Errorf("%w: %s · %s · %s · %s (%s) — правка сделала бы её копией",
				ErrRepeat, tx.Date().Format(time.DateOnly), tx.Amount(),
				repeatSubject(tx), tx.Account(), tx.ID())
		}
		recs[i] = edited
		finance.Sort(recs)
		if err := financejsonl.Save(ledgerPath, recs, time.Now); err != nil {
			return finance.Record{}, err
		}
		return edited, nil
	}
	return finance.Record{}, fmt.Errorf("%w: %s", ErrNoSuchEntry, id)
}

// editText — текстовые поля правки, собранные вместе, чтобы список аргументов
// не превращался в шеренгу одинаковых строк, где легко переставить два соседних.
type editText struct {
	category, sub, place, note, source, account string
}

// editParams превращает флаги в параметры правки.
//
// Пустое значение и «поле стёрли» — разные намерения, и различает их только
// факт передачи флага: `--account=` стирает, отсутствие `--account` не трогает.
func editParams(fs *flag.FlagSet, amount, date string, t editText) (finance.EditParams, error) {
	p := finance.EditParams{
		Category: t.category, Subcategory: t.sub, Place: t.place,
		Description: t.note, Source: t.source, Account: t.account,
		ClearSubcategory: isFlagSet(fs, "sub") && t.sub == "",
		ClearPlace:       isFlagSet(fs, "place") && t.place == "",
		ClearDescription: isFlagSet(fs, "note") && t.note == "",
		ClearAccount:     isFlagSet(fs, "account") && t.account == "",
	}
	if amount != "" {
		money, err := domain.ParseMoney(amount)
		if err != nil {
			return finance.EditParams{}, err
		}
		p.Amount = money
	}
	if date != "" {
		when, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return finance.EditParams{}, fmt.Errorf("--date %q: expected YYYY-MM-DD", date)
		}
		p.Date = when
	}
	return p, nil
}
