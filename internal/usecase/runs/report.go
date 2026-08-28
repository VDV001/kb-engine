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
	"maps"
	"slices"
	"strings"
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
	// Медианы длительности в двух окнах — раннем и позднем, по WindowSize
	// прогонов в каждом. Медиана, а не среднее: один прогон на холодном кэше
	// сдвигает среднее и рисует замедление, которого нет.
	//
	// Окна равны и берутся с краёв: середина отбрасывается намеренно, иначе
	// «позднее» окно у команды с сотнями прогонов растворило бы недавнюю
	// правку в истории. WindowSize нулевой означает «сравнивать не с чем» —
	// у проверки это отдельный ответ, а не повод молча промолчать.
	EarlyMedian time.Duration
	LateMedian  time.Duration
	WindowSize  int
	// MixedShape — в окнах разный состав подкоманд, и медианы сравнивать
	// нельзя. У зонтичных команд (`fin` объединяет spelling и sync, отличаясь
	// впятеро по стоимости) медиана меряет состав, а не скорость: на живом
	// журнале это дало «медленнее в 5,2 раза» там, где ничего не замедлялось.
	//
	// Наружу выходит только этот признак. Сами подкоманды не показываются: они
	// приходят из аргументов, а там лежат настоящие суммы и места владельца, и
	// правило пакета — имена команд и числа, но не значения.
	MixedShape bool
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
	// Tools — вызовы инструментов MCP, чаще спрашиваемые сверху.
	//
	// Отдельным списком, а не вместе с командами, по двум причинам сразу:
	// у вызова нет порога молчания (инструмент зовут, когда о нём спросили,
	// и «давно не звали» ничего не значит), а движок объявил бы его командой,
	// которой больше не знает.
	Tools []CommandStat
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

	byName, since := aggregate(recs)
	r.Since = since
	if !r.Since.IsZero() {
		r.Span = now.Sub(r.Since)
	}

	for _, s := range byName {
		// Вызов инструмента — не команда движка: разбор имени решает это в
		// одном месте, чтобы «mcp:search_catalog» не попал ни в список команд,
		// ни в список забытых.
		if tool, isTool := ToolOf(s.Name); isTool {
			// Наружу отчёт отдаёт ИМЯ ИНСТРУМЕНТА, а не строку журнала:
			// приставка — способ хранения, и показывать её значило бы учить
			// читателя формату файла ради ничего.
			s.Name = tool
			r.Tools = append(r.Tools, s)
			continue
		}
		r.Commands = append(r.Commands, s)
		if !slices.Contains(known, s.Name) {
			r.Unknown = append(r.Unknown, s.Name)
		}
	}
	// Вызовы сортируются по частоте: счётчик заводился ради вопроса «сколько
	// раз агент вообще спрашивал базу», и первым должен стоять ответ на него.
	slices.SortFunc(r.Tools, func(a, b CommandStat) int {
		if c := cmp.Compare(b.Runs, a.Runs); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
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

// aggregate сводит записи по командам и отдаёт момент самой ранней из них.
//
// Горизонт считается здесь же, одним проходом: отдельный проход по тем же
// записям ради одного минимума разошёлся бы с этим, стоило кому-нибудь
// отфильтровать записи в одном месте и забыть в другом.
func aggregate(recs []domain.RunRecord) (map[string]CommandStat, time.Time) {
	byName := map[string]CommandStat{}
	byTime := map[string][]domain.RunRecord{}
	var since time.Time
	for _, rec := range recs {
		if since.IsZero() || rec.StartedAt().Before(since) {
			since = rec.StartedAt()
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
		byTime[rec.Command()] = append(byTime[rec.Command()], rec)
	}
	for name, recs := range byTime {
		s := byName[name]
		s.EarlyMedian, s.LateMedian, s.WindowSize, s.MixedShape = windows(recs)
		byName[name] = s
	}
	return byName, since
}

// windows делит прогоны команды на раннее и позднее окно равного размера и
// отдаёт медианы длительности в каждом.
//
// Записи приходят в порядке журнала — он дозаписывается, поэтому порядок строк
// и есть порядок времени; сортировка по StartedAt дала бы то же самое ценой
// прохода, но перестала бы работать, если часы машины разово ушли назад.
// Порядок файла в этом смысле честнее: он говорит, что движок видел.
//
// maxWindow ограничивает окно сверху: у команды с семьюстами прогонов медиана
// по всей половине истории меняется так медленно, что замедление в ней тонет.
func windows(recs []domain.RunRecord) (early, late time.Duration, size int, mixed bool) {
	const maxWindow = 20
	size = min(len(recs)/2, maxWindow)
	if size == 0 {
		return 0, 0, 0, false
	}
	took := func(rs []domain.RunRecord) time.Duration {
		d := make([]time.Duration, 0, len(rs))
		for _, r := range rs {
			d = append(d, r.Took())
		}
		slices.Sort(d)
		return d[len(d)/2]
	}
	head, tail := recs[:size], recs[len(recs)-size:]
	return took(head), took(tail), size, shapeOf(head) != shapeOf(tail)
}

// shapeOf описывает, КАКУЮ РАБОТУ просили в этом окне: набор имён флагов плюс
// позиционные аргументы. Строка нужна только для сравнения двух окон между
// собой и наружу не отдаётся.
//
// Имена, но не значения. Это не осторожность, а условие работоспособности с
// двух сторон: значения — это суммы и места владельца, и они же уникальны у
// каждого прогона, так что форма по ним различалась бы всегда и замедление не
// посчиталось бы ни разу.
//
// Почему не первое слово, как было сначала: у `audit` первый аргумент —
// `--catalog`, и по нему все прогоны одинаковы, тогда как `--check files`
// (18 мс) и полный набор без флага (374 мс) — работа разной величины. Живой
// журнал дал на этом ложное «стала медленнее в 20 раз».
//
// ponytail: значение флага отбрасывается целиком, поэтому `--check all` и
// `--check files` считаются одной формой. Потолок известен: различать их можно
// белым списком значений, безопасных для показа, — но списка пока нет, а
// угадывать, какое значение не секрет, дороже пропущенной находки.
func shapeOf(recs []domain.RunRecord) string {
	counts := map[string]int{}
	for _, r := range recs {
		parts := map[string]bool{}
		skipNext := false
		for i, a := range r.Args() {
			if skipNext {
				skipNext = false
				continue
			}
			if strings.HasPrefix(a, "-") {
				name, _, _ := strings.Cut(a, "=")
				parts[name] = true
				// Значение флага идёт следующим словом, если оно само не флаг.
				// Пропускаем его, иначе `--check all` внесло бы в форму «all»,
				// а `--place Магнит` — название магазина.
				args := r.Args()
				skipNext = i+1 < len(args) && !strings.HasPrefix(args[i+1], "-")
				continue
			}
			parts[a] = true
		}
		key := strings.Join(slices.Sorted(maps.Keys(parts)), ",")
		counts[key]++
	}
	keys := slices.Sorted(maps.Keys(counts))
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s:%d;", k, counts[k])
	}
	return b.String()
}
