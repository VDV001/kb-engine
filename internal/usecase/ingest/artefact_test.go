package ingest_test

import (
	"strings"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/ingest"
)

func artefactParams(t *testing.T, title, file string) domain.EntryParams {
	t.Helper()
	cat, err := domain.NewCategory("standards")
	if err != nil {
		t.Fatalf("category: %v", err)
	}
	life, err := domain.NewLifecycle("active")
	if err != nil {
		t.Fatalf("lifecycle: %v", err)
	}
	read, err := domain.NewReadState("read")
	if err != nil {
		t.Fatalf("readstate: %v", err)
	}
	return domain.EntryParams{
		Kind: domain.KindArticle, Title: title, Category: cat,
		Lifecycle: life, ReadState: &read, NotesFile: file, Source: "internal",
	}
}

func catalogWith(t *testing.T, entries ...domain.Entry) *domain.Catalog {
	t.Helper()
	c, err := domain.NewCatalog(entries)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return c
}

// Собственный артефакт — файл в базе, у него нет адреса в интернете. Обычный
// путь добавления его отбрасывает: он дедуплицирует по url и молча пропускает
// всё, у чего url пуст.
func TestPlanArtefacts_addsAnEntryWithoutAURL(t *testing.T) {
	c := catalogWith(t)
	p := artefactParams(t, "Harness Engineering Defaults v1.3.0", "standards/harness-engineering-defaults/v1.md")

	added, rep, err := ingest.PlanArtefacts(c, []domain.EntryParams{p}, time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PlanArtefacts: %v", err)
	}
	if rep.Added != 1 || len(added) != 1 {
		t.Fatalf("added = %d (report %+v), want 1", len(added), rep)
	}
	if got := added[0].NotesFile(); got != "standards/harness-engineering-defaults/v1.md" {
		t.Errorf("file = %q", got)
	}
	if added[0].ID() == 0 {
		t.Error("id не выдан")
	}
	if added[0].DateAdded() == nil {
		t.Error("дата добавления не проставлена")
	}
}

// Дедуп идёт по файлу, а не по адресу: второй раз добавленный тот же стандарт
// создал бы две записи об одном файле, и обе были бы «правдой».
func TestPlanArtefacts_skipsAFileAlreadyInTheCatalog(t *testing.T) {
	first, _, err := ingest.PlanArtefacts(catalogWith(t),
		[]domain.EntryParams{artefactParams(t, "Стандарт", "standards/x/v1.md")}, time.Now())
	if err != nil {
		t.Fatalf("первый прогон: %v", err)
	}

	_, rep, err := ingest.PlanArtefacts(catalogWith(t, first...),
		[]domain.EntryParams{artefactParams(t, "Стандарт ещё раз", "standards/x/v1.md")}, time.Now())
	if err != nil {
		t.Fatalf("второй прогон: %v", err)
	}
	if rep.Added != 0 || rep.SkippedDuplicate != 1 {
		t.Errorf("report = %+v, want Added=0 SkippedDuplicate=1", rep)
	}
}

// Дубль внутри одной партии ловится там же: иначе две одинаковые строки в
// одном вызове дали бы две записи.
func TestPlanArtefacts_skipsADuplicateWithinTheBatch(t *testing.T) {
	p := artefactParams(t, "Стандарт", "standards/x/v1.md")

	_, rep, err := ingest.PlanArtefacts(catalogWith(t), []domain.EntryParams{p, p}, time.Now())
	if err != nil {
		t.Fatalf("PlanArtefacts: %v", err)
	}
	if rep.Added != 1 || rep.SkippedDuplicate != 1 {
		t.Errorf("report = %+v, want Added=1 SkippedDuplicate=1", rep)
	}
}

// Артефакт без файла — это запись, которая ни на что не указывает. Молча
// пропустить её значит отчитаться об успехе, ничего не добавив.
func TestPlanArtefacts_refusesAnEntryWithoutAFile(t *testing.T) {
	p := artefactParams(t, "Без файла", "")

	if _, _, err := ingest.PlanArtefacts(catalogWith(t), []domain.EntryParams{p}, time.Now()); err == nil {
		t.Fatal("ожидалась ошибка: артефакту нужен файл")
	} else if !strings.Contains(err.Error(), "Без файла") {
		t.Errorf("ошибка не называет запись: %v", err)
	}
}

// Нарушение инварианта обязано остановить всю партию: половина применённых
// изменений хуже, чем ни одного.
func TestPlanArtefacts_abortsTheWholeBatchOnAnInvalidEntry(t *testing.T) {
	good := artefactParams(t, "Хороший", "standards/a/v1.md")
	bad := artefactParams(t, "", "standards/b/v1.md") // пустой заголовок

	added, _, err := ingest.PlanArtefacts(catalogWith(t), []domain.EntryParams{good, bad}, time.Now())
	if err == nil {
		t.Fatal("ожидалась ошибка на записи без заголовка")
	}
	if added != nil {
		t.Errorf("вернулось %d записей — партия должна отменяться целиком", len(added))
	}
}
