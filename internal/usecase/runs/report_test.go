package runs_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/runs"
)

// known — набор команд, который движок знает о себе. В боевом вызове он
// приходит из карты диспетчера, а не из списка, набранного руками: список
// расходится с кодом молча, и тогда «не запускалась ни разу» сказали бы про
// команду, которой больше нет.
var known = []string{"audit", "drift", "fin", "serve", "version"}

// journalStub подставляет журнал, не трогая диск.
type journalStub struct {
	recs       []domain.RunRecord
	unreadable int
	exists     bool
	err        error
}

func (j journalStub) Load() ([]domain.RunRecord, int, error) {
	return j.recs, j.unreadable, j.err
}

func (j journalStub) Exists() (bool, error) { return j.exists, j.err }

func at(t *testing.T, command string, when time.Time, code int, args ...string) domain.RunRecord {
	t.Helper()
	now := when.Add(time.Hour)
	rec, err := domain.NewRunRecord(command, args, when, 5*time.Millisecond, code, now)
	if err != nil {
		t.Fatalf("NewRunRecord(%q): %v", command, err)
	}
	return rec
}

func TestBuild(t *testing.T) {
	now := time.Date(2026, 8, 19, 15, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return now.AddDate(0, 0, -d) }

	tests := []struct {
		name    string
		journal journalStub
		want    func(t *testing.T, r runs.Report)
	}{
		{
			// Журнала нет вовсе — движок ни разу его не писал. Это НЕ то же
			// самое, что пустой журнал: там движок писал и не запускал команд.
			name:    "журнала нет",
			journal: journalStub{exists: false},
			want: func(t *testing.T, r runs.Report) {
				if r.Exists {
					t.Error("журнала нет, а отчёт говорит, что есть")
				}
				if r.Total != 0 {
					t.Errorf("записей %d, ожидалось 0", r.Total)
				}
				if len(r.NeverRan) != len(known) {
					t.Errorf("не запускалось %d команд, ожидалось %d", len(r.NeverRan), len(known))
				}
			},
		},
		{
			name:    "журнал есть и пуст",
			journal: journalStub{exists: true},
			want: func(t *testing.T, r runs.Report) {
				if !r.Exists {
					t.Error("журнал есть, а отчёт говорит, что нет")
				}
				if len(r.NeverRan) != len(known) {
					t.Errorf("не запускалось %d команд, ожидалось %d", len(r.NeverRan), len(known))
				}
			},
		},
		{
			name: "прогоны считаются по команде, отказы отдельно",
			journal: journalStub{exists: true, recs: []domain.RunRecord{
				at(t, "fin", day(3), 0, "add"),
				at(t, "fin", day(1), 1, "sync"),
				at(t, "fin", day(0), 0, "list"),
				at(t, "audit", day(9), 0, "--check", "all"),
			}},
			want: func(t *testing.T, r runs.Report) {
				if r.Total != 4 {
					t.Errorf("записей %d, ожидалось 4", r.Total)
				}
				fin, ok := stat(r, "fin")
				if !ok {
					t.Fatal("fin в отчёте не нашлась")
				}
				if fin.Runs != 3 {
					t.Errorf("fin прогонов %d, ожидалось 3", fin.Runs)
				}
				if fin.Failures != 1 {
					t.Errorf("fin отказов %d, ожидалось 1", fin.Failures)
				}
				if !fin.LastRun.Equal(day(0)) {
					t.Errorf("fin последний прогон %v, ожидался %v", fin.LastRun, day(0))
				}
				// Первая запись журнала задаёт горизонт: без него «ни разу»
				// читается как «никогда», а это утверждение о движке, которого
				// журнал сделать не может.
				if !r.Since.Equal(day(9)) {
					t.Errorf("журнал ведётся с %v, ожидалось %v", r.Since, day(9))
				}
			},
		},
		{
			// Давние сверху: так «давно не запускали» видно без выдуманного
			// порога, а выдуманный порог красит зелёное в красное и учит не
			// смотреть на предупреждения.
			name: "порядок — по давности последнего прогона",
			journal: journalStub{exists: true, recs: []domain.RunRecord{
				at(t, "fin", day(0), 0),
				at(t, "audit", day(9), 0),
				at(t, "drift", day(4), 0),
			}},
			want: func(t *testing.T, r runs.Report) {
				got := make([]string, 0, len(r.Commands))
				for _, c := range r.Commands {
					got = append(got, c.Name)
				}
				wantOrder := []string{"audit", "drift", "fin"}
				if !slices.Equal(got, wantOrder) {
					t.Errorf("порядок %v, ожидался %v", got, wantOrder)
				}
			},
		},
		{
			// Журнал переживает переименование команды. Молча выбросить такую
			// запись значило бы соврать про число прогонов.
			name: "команда из журнала, которой движок больше не знает",
			journal: journalStub{exists: true, recs: []domain.RunRecord{
				at(t, "старая-команда", day(2), 0),
				at(t, "fin", day(1), 0),
			}},
			want: func(t *testing.T, r runs.Report) {
				if !slices.Contains(r.Unknown, "старая-команда") {
					t.Errorf("незнакомая команда не названа: %v", r.Unknown)
				}
				if _, ok := stat(r, "старая-команда"); !ok {
					t.Error("незнакомая команда выброшена из прогонов")
				}
				if slices.Contains(r.NeverRan, "fin") {
					t.Error("fin запускалась, а попала в «ни разу»")
				}
			},
		},
		{
			// Нечитаемая строка обязана доехать числом: одна оборванная запись
			// делает выводы неполными, и молчание об этом — ровно то, от чего
			// журнал заведён.
			name:    "нечитаемые строки названы",
			journal: journalStub{exists: true, unreadable: 2},
			want: func(t *testing.T, r runs.Report) {
				if r.Unreadable != 2 {
					t.Errorf("нечитаемых %d, ожидалось 2", r.Unreadable)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := runs.Build(tt.journal, known, now)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			tt.want(t, r)
		})
	}
}

// Отказ журнала не выдумывает пустого отчёта: пустой отчёт читается как
// «прогонов не было», а это худший ответ для проверки, заведённой ради
// вопроса «запускали ли».
func TestBuild_journalError(t *testing.T) {
	boom := errors.New("журнал не прочитался")
	_, err := runs.Build(journalStub{exists: true, err: boom}, known, time.Now())
	if !errors.Is(err, boom) {
		t.Fatalf("ошибка %v, ожидалась %v", err, boom)
	}
}

func stat(r runs.Report, name string) (runs.CommandStat, bool) {
	for _, c := range r.Commands {
		if c.Name == name {
			return c, true
		}
	}
	return runs.CommandStat{}, false
}
