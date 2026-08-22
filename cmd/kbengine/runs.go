package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/runlogjsonl"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// journalFile — журнал на диске под портом, который спрашивает usecase.
type journalFile struct {
	path string
	now  func() time.Time
}

func (j journalFile) Load() ([]domain.RunRecord, int, error) {
	return runlogjsonl.Load(j.path, j.now)
}

func (j journalFile) Exists() (bool, error) { return runlogjsonl.Exists(j.path) }

// Команда регистрируется здесь, а не в литерале карты команд: отчёт читает саму
// карту, чтобы знать, чего движок ещё не запускал, а Go запрещает такую петлю в
// инициализаторе. Регистрация в init её разрывает и оставляет карту единственным
// источником списка — вписанный рядом список разошёлся бы с ней молча.
func init() { commands["runs"] = withoutStdin(runRuns) }

// runRuns печатает, что движок делал и чего не делал.
//
// Отчёт существует ради вопроса, на который до него отвечала только память
// человека: «эту проверку прогоняли?». За август память проиграла трижды —
// валидатор карт, который не вызывал никто; сторож каталога, не запускавшийся
// ни разу; отгружаемый образ, который никто не ЗАПУСКАЛ. Каждый раз тишина
// читалась как «всё в порядке».
//
// Каталог команде не нужен, поэтому она верхнего уровня, а не под `audit`:
// требовать флаг, который не используется, значит учить передавать его не глядя.
func runRuns(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	journalPath := fs.String("journal", "", "path to the run journal (default: $KBENGINE_RUNLOG or the XDG state dir)")
	check := fs.Bool("check", false, "проверить инварианты и выйти с кодом 1, если есть находки")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	path := *journalPath
	if path == "" {
		p, err := runlogjsonl.DefaultPath(os.Getenv)
		if err != nil {
			fmt.Fprintf(stderr, "runs: %v\n", err)
			return 1
		}
		path = p
	}

	// Известные команды берутся из карты диспетчера, а не из списка здесь:
	// список расходится с кодом молча, и новая команда никогда не попала бы в
	// «не запускалась ни разу» — то есть отчёт молчал бы именно там, где он и
	// заведён говорить.
	known := slices.Sorted(maps.Keys(commands))

	rep, err := runs.Build(journalFile{path: path, now: time.Now}, known, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "runs: %v\n", err)
		return 1
	}
	if *check {
		return printChecks(stdout, path, rep, time.Now())
	}
	printReport(stdout, path, rep, time.Now())
	return 0
}

// printChecks печатает нарушенные инварианты и отвечает КОДОМ.
//
// Код важнее вывода: отчёт читает человек, а код читает расписание, и без него
// сторож не отличит «нашлось» от «прогнали». Именно этим болел audit_watch —
// написан и не вызывался никем, потому что позвать его было нечем.
//
// Порог живёт в DefaultPolicy и там же объяснён. Флага для его правки нет
// намеренно: порог, который каждый передаёт свой, перестаёт быть общим правилом
// и превращается в способ выключить находку, не признав её.
func printChecks(w io.Writer, path string, r runs.Report, now time.Time) int {
	p := runs.DefaultPolicy()
	// Знание живёт здесь, потому что здесь и принято: `runs --check` отвечает
	// кодом 1, когда находки ЕСТЬ. Без этой строки инвариант «падает» считал бы
	// каждый успешный прогон сторожа отказом — на живом журнале он так и
	// заявил «runs падает в 65 % прогонов».
	p.FindingsExit = []string{"runs"}
	found := runs.Check(r, p, now)
	fmt.Fprintf(w, "журнал прогонов: %s\n", path)
	fmt.Fprintf(w, "пороги: молчание %s · отказы %.0f%% при %d+ прогонах · замедление %.1f× при окне %d+\n",
		human(p.Stale), 100*p.FailureRate, p.MinRuns, p.SlowFactor, p.MinWindow)
	if len(found) == 0 {
		fmt.Fprintln(w, "инварианты не нарушены.")
		return 0
	}
	for _, f := range found {
		fmt.Fprintf(w, "  [%s] %s\n", f.Subject, f.Reason)
	}
	fmt.Fprintf(w, "находок: %d\n", len(found))
	return 1
}

// human повторяет формат сроков из usecase, чтобы шапка и находки говорили об
// одном и том же одинаково.
func human(d time.Duration) string {
	if days := int(d.Hours() / 24); days >= 2 {
		return fmt.Sprintf("%d дн.", days)
	}
	return fmt.Sprintf("%d ч.", int(d.Hours()))
}

// printReport выводит отчёт человеку.
//
// ⚠️ Наружу идут ИМЕНА команд и числа. Значения аргументов журнал хранит
// целиком (решение владельца 19.08.2026: файл лежит вне любого репозитория),
// но в них настоящие суммы и места владельца, поэтому в отчёт они не попадают
// вовсе — хранение и показ здесь разные вопросы.
func printReport(w io.Writer, path string, r runs.Report, now time.Time) {
	if !r.Exists {
		fmt.Fprintf(w, "журнал прогонов не найден: %s\n", path)
		fmt.Fprintln(w, "движок ни разу его не писал — это первый запуск или сборка старше журнала.")
		fmt.Fprintln(w, "сказать «прогонов не было» здесь нельзя: движок мог работать и не вести записей.")
		return
	}

	fmt.Fprintf(w, "журнал прогонов: %s\n", path)
	if r.Total == 0 {
		fmt.Fprintln(w, "журнал заведён, но в нём ни одного прогона.")
	} else {
		fmt.Fprintf(w, "ведётся с %s (%s) · записей %d\n",
			r.Since.Format("02.01.2006"), ago(r.Since, now), r.Total)
	}
	if r.Unreadable > 0 {
		fmt.Fprintf(w, "⚠️ нечитаемых строк: %d — выводы ниже неполны на эти прогоны\n", r.Unreadable)
	}

	if len(r.Commands) > 0 {
		fmt.Fprintln(w, "\nпрогонялось (давно не запускавшиеся сверху):")
		for _, c := range r.Commands {
			line := fmt.Sprintf("  %-14s прогонов %-4d последний %s (%s)",
				c.Name, c.Runs, c.LastRun.Format("02.01.2006"), ago(c.LastRun, now))
			if c.Failures > 0 {
				line += fmt.Sprintf(" · отказов %d", c.Failures)
			}
			if c.LastCode != 0 {
				line += fmt.Sprintf(" · последний код %d", c.LastCode)
			}
			fmt.Fprintln(w, line)
		}
	}

	if len(r.Unknown) > 0 {
		fmt.Fprintf(w, "\nв журнале есть команды, которых движок больше не знает: %s\n",
			strings.Join(r.Unknown, ", "))
		fmt.Fprintln(w, "  переименование или удаление — записи о них остаются правдой о прошлом.")
	}

	if len(r.NeverRan) > 0 {
		fmt.Fprintf(w, "\nне запускалось ни разу с начала журнала — %d из %d:\n",
			len(r.NeverRan), len(r.NeverRan)+len(r.Commands)-len(r.Unknown))
		fmt.Fprintf(w, "  %s\n", strings.Join(r.NeverRan, ", "))
		// Горизонт называется рядом со списком, а не в конце: «ни разу» без
		// него читается как утверждение о движке, тогда как это утверждение о
		// журнале, и на молодом журнале список говорит только о его возрасте.
		if r.Total > 0 {
			fmt.Fprintf(w, "  «ни разу» означает «с начала журнала» (%s), а не «никогда».\n",
				r.Since.Format("02.01.2006"))
		}
	}

	// Правило 11: инструмент называет, чего он НЕ проверял. Без этого абзаца
	// молчание про замедление читается как «замедления нет».
	fmt.Fprintln(w, "\nчего этот отчёт не проверяет:")
	fmt.Fprintln(w, "  · замедление команд — порог пришлось бы выдумать, а выдуманный порог")
	fmt.Fprintln(w, "    красит зелёное в красное и за две недели учит не смотреть на предупреждения;")
	fmt.Fprintln(w, "  · «команда отчиталась успехом, ничего не изменив» — диспетчер не знает,")
	fmt.Fprintln(w, "    изменила ли команда данные, и канала от команд к нему пока нет;")
	fmt.Fprintln(w, "  · что было до первой записи журнала — старее себя он не помнит;")
	fmt.Fprintln(w, "  · текущий прогон — запись о нём появится после того, как команда завершится.")
}

// ago — насколько давно это было, в днях.
//
// Число дней пишется сокращением «дн.» намеренно: правило склонения в проекте
// уже есть (internal/usecase/freshness), и вторая его копия однажды разойдётся
// с первой — это ровно тот класс, который стоил движку трёх реализаций «момента
// записи». Сокращение обходится без правила вовсе.
func ago(t, now time.Time) string {
	days := int(now.Sub(t).Hours() / 24)
	switch {
	case days <= 0:
		return "сегодня"
	case days == 1:
		return "вчера"
	default:
		return fmt.Sprintf("%d дн. назад", days)
	}
}
