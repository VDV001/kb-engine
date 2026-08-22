package runs_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/usecase/runs"
)

func day(n int) time.Time {
	return time.Date(2026, 8, n, 12, 0, 0, 0, time.UTC)
}

// policy с явными порогами: у проверки не должно быть умолчаний, вписанных в
// её собственный код — иначе порог нельзя ни увидеть, ни оспорить.
func policy() runs.Policy {
	return runs.Policy{
		Stale:       7 * 24 * time.Hour,
		FailureRate: 0.30,
		MinRuns:     5,
		SlowFactor:  2.0,
		MinWindow:   4,
	}
}

// Инвариант 1: команда, которую не звали дольше порога, названа вслух.
// Ради этого журнал и заводился — за август память трижды проиграла вопросу
// «эту проверку прогоняли?».
func TestCheck_staleCommandIsNamed(t *testing.T) {
	rep := runs.Report{
		Exists: true, Total: 10, Since: day(1), Span: 21 * 24 * time.Hour,
		Commands: []runs.CommandStat{
			{Name: "drift", Runs: 3, LastRun: day(5)},
			{Name: "audit", Runs: 7, LastRun: day(21)},
		},
	}
	got := runs.Check(rep, policy(), day(22))
	if !hasFinding(got, "drift", "давно") {
		t.Fatalf("drift не запускали 17 дней при пороге 7 — находки нет: %v", names(got))
	}
	if hasFinding(got, "audit", "давно") {
		t.Fatalf("audit запускали вчера, находки быть не должно: %v", names(got))
	}
}

// Инвариант 1б: команда, которую движок знает, но не звали ни разу.
func TestCheck_neverRanIsNamed(t *testing.T) {
	rep := runs.Report{Exists: true, Total: 5, Since: day(1), NeverRan: []string{"dedup", "migrate"}}
	got := runs.Check(rep, policy(), day(22))
	for _, want := range []string{"dedup", "migrate"} {
		if !hasFinding(got, want, "ни разу") {
			t.Fatalf("%q не запускалась ни разу — находки нет: %v", want, names(got))
		}
	}
}

// Инвариант 2: команда, падающая чаще порога, названа — но только когда
// прогонов достаточно, чтобы доля что-то значила.
func TestCheck_failingCommandIsNamed(t *testing.T) {
	rep := runs.Report{
		Exists: true, Total: 20, Since: day(1),
		Commands: []runs.CommandStat{
			{Name: "fin", Runs: 10, Failures: 4, LastRun: day(21)},
			{Name: "set", Runs: 10, Failures: 1, LastRun: day(21)},
			{Name: "runs", Runs: 2, Failures: 2, LastRun: day(21)}, // прогонов мало
		},
	}
	got := runs.Check(rep, policy(), day(22))
	if !hasFinding(got, "fin", "падает") {
		t.Fatalf("fin падает в 40%% прогонов при пороге 30 — находки нет: %v", names(got))
	}
	if hasFinding(got, "set", "падает") {
		t.Fatalf("set падает в 10%% — находки быть не должно: %v", names(got))
	}
	if hasFinding(got, "runs", "падает") {
		t.Fatalf("у runs два прогона — доля ничего не значит, находки быть не должно: %v", names(got))
	}
}

// Инвариант 3: замедление. Сравниваются последние прогоны с прежними, и только
// когда в обоих окнах хватает данных.
func TestCheck_slowdownIsNamed(t *testing.T) {
	rep := runs.Report{
		Exists: true, Total: 16, Since: day(1),
		Commands: []runs.CommandStat{
			{Name: "search", Runs: 8, LastRun: day(21),
				EarlyMedian: 40 * time.Millisecond, LateMedian: 120 * time.Millisecond, WindowSize: 4},
			{Name: "audit", Runs: 8, LastRun: day(21),
				EarlyMedian: 20 * time.Millisecond, LateMedian: 22 * time.Millisecond, WindowSize: 4},
			{Name: "set", Runs: 4, LastRun: day(21),
				EarlyMedian: 10 * time.Millisecond, LateMedian: 90 * time.Millisecond, WindowSize: 2},
		},
	}
	got := runs.Check(rep, policy(), day(22))
	if !hasFinding(got, "search", "медленнее") {
		t.Fatalf("search замедлился втрое при пороге 2× — находки нет: %v", names(got))
	}
	if hasFinding(got, "audit", "медленнее") {
		t.Fatalf("audit не замедлялся — находки быть не должно: %v", names(got))
	}
	if hasFinding(got, "set", "медленнее") {
		t.Fatalf("у set окно из двух прогонов — сравнивать нельзя: %v", names(got))
	}
}

// Проверка обязана говорить, чего она НЕ проверяла: пустой журнал и журнал без
// нужных полей снаружи выглядят как «всё в порядке», а означают разное.
func TestCheck_saysWhatItCouldNotCheck(t *testing.T) {
	got := runs.Check(runs.Report{Exists: false}, policy(), day(22))
	if len(got) == 0 {
		t.Fatal("журнала нет вовсе — проверка обязана сказать это, а не молчать")
	}
	if !hasFinding(got, "журнал", "") {
		t.Fatalf("отсутствие журнала не названо: %v", names(got))
	}
}

func hasFinding(fs []runs.Finding, subject, word string) bool {
	for _, f := range fs {
		if strings.Contains(f.Subject, subject) && (word == "" || strings.Contains(f.Reason, word)) {
			return true
		}
	}
	return false
}

func names(fs []runs.Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Subject+": "+f.Reason)
	}
	return out
}

// Замедление у зонтичной команды — ловушка, которую нашёл живой прогон, а не
// рассуждение: `fin` объединяет spelling (7 мс) и sync (26 мс), и медиана
// меряет СОСТАВ подкоманд, а не скорость. На живом журнале это дало
// «медленнее в 5,2 раза» там, где ничего не замедлялось.
//
// Наружу состав не выходит: проверка знает лишь, что он различается.
func TestCheck_mixedShapeBlocksSlowdown(t *testing.T) {
	rep := runs.Report{
		Exists: true, Total: 16, Since: day(1),
		Commands: []runs.CommandStat{
			{Name: "fin", Runs: 8, LastRun: day(21),
				EarlyMedian: 5 * time.Millisecond, LateMedian: 26 * time.Millisecond,
				WindowSize: 4, MixedShape: true},
		},
	}
	got := runs.Check(rep, policy(), day(22))
	if hasFinding(got, "fin", "медленнее") {
		t.Fatalf("состав подкоманд в окнах разный — замедление считать нельзя: %v", names(got))
	}
	if !hasFinding(got, "fin", "сравнивать") {
		t.Fatalf("отказ сравнивать обязан быть НАЗВАН, а не молчалив: %v", names(got))
	}
}
