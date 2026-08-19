// Package runs answers questions about what the engine actually did, reading
// the journal of runs instead of asking the engine to report on itself.
//
// Смысл пакета — Правило 11 в рантайме: инструмент обязан называть, чего он НЕ
// проверял. За август трижды ловилось одно и то же — валидатор карт, который
// не вызывал никто; сторож каталога, не запускавшийся ни разу с момента
// написания; отгружаемый образ, который до появления джобы k8s никто не
// ЗАПУСКАЛ. Каждый раз тишина читалась как «всё в порядке».
//
// ⚠️ В аргументах журнала лежат настоящие суммы и места владельца. Отчёт
// оперирует ИМЕНАМИ команд и числами; значения аргументов сюда не попадают
// вовсе — хранение и показ здесь разные вопросы (см. пакет runlogjsonl).
package runs

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
)

// Journal is the port to the run journal. Интерфейс живёт в пакете-потребителе,
// а не рядом с реализацией: направление зависимости решает потребитель.
type Journal interface {
	// Load returns the records and how many lines could not be read.
	Load() ([]domain.RunRecord, int, error)
	// Exists reports whether the journal is on disk at all.
	Exists() (bool, error)
}

// CommandStat — что журнал знает про одну команду.
type CommandStat struct {
	Name     string
	Runs     int
	Failures int // прогонов с ненулевым кодом возврата
	LastRun  time.Time
	LastCode int
}

// Report — ответ журнала на вопрос «что движок делал и чего не делал».
type Report struct {
	// Exists различает два молчания: журнала нет вовсе (движок ни разу его не
	// писал — старая сборка или первый запуск) и журнал есть, но команду не
	// звали. Снаружи оба выглядят как ноль прогонов, а означают разное.
	Exists     bool
	Total      int
	Unreadable int
	// Since — момент первой записи. Горизонт называется до любого «ни разу»:
	// без него «drift не запускался» читается как утверждение о движке, тогда
	// как это утверждение о журнале.
	Since time.Time
	// Span — сколько журнал ведётся к моменту отчёта.
	Span time.Duration
	// Commands — прогонявшиеся команды, давние сверху.
	Commands []CommandStat
	// NeverRan — известные движку команды без единого прогона, по алфавиту.
	NeverRan []string
	// Unknown — команды из журнала, которых движок больше не знает.
	Unknown []string
}

// Build reads the journal and answers what ran and what did not.
//
// known приходит от вызывающего — это карта диспетчера, а не список, набранный
// здесь руками: список расходится с кодом молча, и тогда отчёт уверенно
// сообщал бы «не запускалась ни разу» про команду, которой больше нет.
func Build(j Journal, known []string, now time.Time) (Report, error) {
	exists, err := j.Exists()
	if err != nil {
		return Report{}, fmt.Errorf("runs: не проверить журнал: %w", err)
	}
	recs, unreadable, err := j.Load()
	if err != nil {
		return Report{}, fmt.Errorf("runs: не прочитать журнал: %w", err)
	}

	r := Report{Exists: exists, Total: len(recs), Unreadable: unreadable}

	byName := map[string]CommandStat{}
	for _, rec := range recs {
		if r.Since.IsZero() || rec.StartedAt().Before(r.Since) {
			r.Since = rec.StartedAt()
		}
		s := byName[rec.Command()]
		s.Name = rec.Command()
		s.Runs++
		if rec.ExitCode() != 0 {
			s.Failures++
		}
		if s.LastRun.IsZero() || rec.StartedAt().After(s.LastRun) {
			s.LastRun = rec.StartedAt()
			s.LastCode = rec.ExitCode()
		}
		byName[rec.Command()] = s
	}
	if !r.Since.IsZero() {
		r.Span = now.Sub(r.Since)
	}

	for _, s := range byName {
		r.Commands = append(r.Commands, s)
		if !slices.Contains(known, s.Name) {
			r.Unknown = append(r.Unknown, s.Name)
		}
	}
	// Давние сверху: так «давно не запускали» видно без порога. Выдуманный
	// порог красит зелёное в красное и за две недели учит не смотреть на
	// предупреждения — цена, названная правилом 12.
	slices.SortFunc(r.Commands, func(a, b CommandStat) int {
		if c := a.LastRun.Compare(b.LastRun); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	slices.Sort(r.Unknown)

	for _, name := range known {
		if _, ran := byName[name]; !ran {
			r.NeverRan = append(r.NeverRan, name)
		}
	}
	slices.Sort(r.NeverRan)
	return r, nil
}
