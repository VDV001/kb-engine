package runs

import (
	"fmt"
	"sort"
	"time"
)

// Finding — нарушенный инвариант так, как его читает человек.
//
// Своя структура, а не audit.Finding: та привязана к записи каталога полем
// EntryID, а здесь предмет — команда движка. Общий тип пришлось бы натягивать
// на оба случая, и одно из полей всегда врало бы пустотой.
type Finding struct {
	Subject string // команда или сам журнал
	Reason  string
}

// Policy — пороги, при которых инвариант считается нарушенным.
//
// ⚠️ Все пять чисел — СУЖДЕНИЯ, а не замеры, и потому живут снаружи проверки:
// порог, вписанный в её код, нельзя ни увидеть, ни оспорить. Значения по
// умолчанию даёт DefaultPolicy, и она же объясняет каждое.
type Policy struct {
	// Stale — сколько команда может не запускаться, прежде чем об этом скажут.
	Stale time.Duration
	// FailureRate — доля прогонов с ненулевым кодом, выше которой это уже не
	// случайность.
	FailureRate float64
	// MinRuns — сколько нужно прогонов, чтобы доля отказов что-то значила.
	MinRuns int
	// SlowFactor — во сколько раз позднее окно должно быть медленнее раннего.
	SlowFactor float64
	// MinWindow — сколько прогонов нужно в каждом окне, чтобы медианы сравнивать.
	MinWindow int
}

// DefaultPolicy — пороги по умолчанию, каждый с названной ценой.
func DefaultPolicy() Policy {
	return Policy{
		// Неделя: короче — и ежедневный отчёт ругался бы на команды, которые
		// зовут по случаю (dedup, migrate), а такая ругань перестаёт читаться.
		Stale: 7 * 24 * time.Hour,
		// Треть: у команды, падающей в трети прогонов, дело не в руках.
		// Замер живого журнала на 22.08: fin 31 отказ из 92 — ровно этот случай.
		FailureRate: 0.30,
		// Пять: при двух прогонах «100 % отказов» означает две опечатки подряд.
		MinRuns: 5,
		// Вдвое: бенчмарки движка показали, что шум на общей машине перекрывает
		// разницу в полтора раза, и гейт по времени поэтому не заводили вовсе.
		SlowFactor: 2.0,
		// Четыре: медиана по двум — это среднее двух, а по одному сравнению
		// делать вывод о замедлении нельзя.
		MinWindow: 4,
	}
}

// Check задаёт журналу вопросы, на которые до него отвечала память человека.
//
// Порядок находок: сначала то, что мешает проверять (журнала нет), потом
// молчащие команды, потом падающие, потом замедлившиеся. Внутри группы — по
// алфавиту, чтобы отчёт не переставлялся между прогонами.
func Check(rep Report, p Policy, now time.Time) []Finding {
	// Журнала нет вовсе — единственный ответ, который стоит дать до всех
	// остальных: без него «команда не запускалась» говорит о журнале, а не о
	// команде, и читать такой список опаснее, чем не читать.
	if !rep.Exists {
		return []Finding{{
			Subject: "журнал прогонов",
			Reason:  "его нет на диске — проверять нечего; либо движок ни разу не запускался этой сборкой, либо журнал пишется в другое место",
		}}
	}

	var out []Finding
	if rep.Unreadable > 0 {
		out = append(out, Finding{
			Subject: "журнал прогонов",
			Reason:  fmt.Sprintf("%d строк не прочитано — эти прогоны в проверку не вошли", rep.Unreadable),
		})
	}

	never := append([]string(nil), rep.NeverRan...)
	sort.Strings(never)
	for _, name := range never {
		out = append(out, Finding{
			Subject: name,
			Reason:  fmt.Sprintf("не запускалась ни разу за %s наблюдения", human(rep.Span)),
		})
	}

	stale, failing, slow := groups(rep, p, now)
	out = append(out, stale...)
	out = append(out, failing...)
	out = append(out, slow...)
	return out
}

func groups(rep Report, p Policy, now time.Time) (stale, failing, slow []Finding) {
	cmds := append([]CommandStat(nil), rep.Commands...)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	for _, c := range cmds {
		if p.Stale > 0 && !c.LastRun.IsZero() && now.Sub(c.LastRun) > p.Stale {
			stale = append(stale, Finding{
				Subject: c.Name,
				Reason: fmt.Sprintf("давно не запускалась: последний раз %s назад, порог %s",
					human(now.Sub(c.LastRun)), human(p.Stale)),
			})
		}
		// Доля считается только при достаточном числе прогонов: у команды с
		// двумя запусками «100 % отказов» — это две опечатки, а не диагноз.
		if c.Runs >= p.MinRuns && float64(c.Failures)/float64(c.Runs) > p.FailureRate {
			failing = append(failing, Finding{
				Subject: c.Name,
				Reason: fmt.Sprintf("падает в %d прогонах из %d (%.0f%%), порог %.0f%%",
					c.Failures, c.Runs, 100*float64(c.Failures)/float64(c.Runs), 100*p.FailureRate),
			})
		}
		// Отказ сравнивать НАЗЫВАЕТСЯ, а не молчит: молчаливый отказ снаружи
		// выглядит как «проверено, чисто» — то самое, против чего заведён весь
		// журнал. Условие стоит до порога замедления: иначе зонтичная команда,
		// у которой состав разъехался, сперва получила бы ложную находку.
		if c.MixedShape && c.WindowSize >= p.MinWindow {
			slow = append(slow, Finding{
				Subject: c.Name,
				Reason:  "замедление сравнивать нельзя: в ранних и поздних прогонах разный состав подкоманд, и медиана меряла бы состав, а не скорость",
			})
			continue
		}
		if c.WindowSize >= p.MinWindow && c.EarlyMedian > 0 &&
			float64(c.LateMedian) > p.SlowFactor*float64(c.EarlyMedian) {
			slow = append(slow, Finding{
				Subject: c.Name,
				Reason: fmt.Sprintf("стала медленнее в %.1f раза: было %s, стало %s (медианы по %d прогонам с краёв)",
					float64(c.LateMedian)/float64(c.EarlyMedian), c.EarlyMedian, c.LateMedian, c.WindowSize),
			})
		}
	}
	return stale, failing, slow
}

// human печатает срок так, как о нём говорят вслух: дни, а не 408h0m0s.
func human(d time.Duration) string {
	switch days := int(d.Hours() / 24); {
	case days >= 2:
		return fmt.Sprintf("%d дн.", days)
	case d >= time.Hour:
		return fmt.Sprintf("%d ч.", int(d.Hours()))
	default:
		return fmt.Sprintf("%d мин.", int(d.Minutes()))
	}
}
