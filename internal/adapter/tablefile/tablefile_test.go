package tablefile_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"

	"github.com/daniil/kb-engine/internal/adapter/tablefile"
)

var want = [][]string{
	{"Ниша", "Продажи, шт", "Рост к году, %"},
	{"Куртки зимние мужские", "182000", "0.12"},
	{"Варежки вязаные", "58000", "-0.06"},
}

func TestRead_xlsx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ниши.xlsx")
	f := excelize.NewFile()
	for r, row := range want {
		for c, v := range row {
			cellName, _ := excelize.CoordinatesToCellName(c+1, r+1)
			var val any = v
			if r > 0 && c > 0 { // числа — числами, как их пишет сервис
				val = map[string]float64{"182000": 182000, "58000": 58000, "0.12": 0.12, "-0.06": -0.06}[v]
			}
			if err := f.SetCellValue("Sheet1", cellName, val); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	got, err := tablefile.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !equal(got, want) {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestRead_csv(t *testing.T) {
	cp1251, err := charmap.Windows1251.NewEncoder().String("Ниша;Продажи, шт;Рост к году, %\nКуртки зимние мужские;182000;0.12\nВарежки вязаные;58000;-0.06\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body string }{
		{"точка с запятой и BOM", "\xef\xbb\xbfНиша;Продажи, шт;Рост к году, %\r\nКуртки зимние мужские;182000;0.12\r\nВарежки вязаные;58000;-0.06\r\n"},
		{"табуляция", "Ниша\tПродажи, шт\tРост к году, %\nКуртки зимние мужские\t182000\t0.12\nВарежки вязаные\t58000\t-0.06\n"},
		{"запятая и кавычки", "Ниша,\"Продажи, шт\",\"Рост к году, %\"\nКуртки зимние мужские,182000,0.12\nВарежки вязаные,58000,-0.06\n"},
		{"Windows-1251", cp1251},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ниши.csv")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := tablefile.Read(path)
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if !equal(got, want) {
				t.Errorf("rows = %q, want %q", got, want)
			}
		})
	}
}

func TestRead_refuses(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "ниши.docx")
	if err := os.WriteFile(doc, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tablefile.Read(doc); !errors.Is(err, tablefile.ErrUnsupported) {
		t.Errorf("docx: err = %v, want ErrUnsupported", err)
	}
	big := filepath.Join(dir, "big.csv")
	if err := os.WriteFile(big, make([]byte, tablefile.MaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tablefile.Read(big); !errors.Is(err, tablefile.ErrTooBig) {
		t.Errorf("big: err = %v, want ErrTooBig", err)
	}
	if _, err := tablefile.Read(filepath.Join(dir, "нет.csv")); err == nil {
		t.Error("missing file read without error")
	}
}

func equal(a, b [][]string) bool {
	return slices.EqualFunc(a, b, func(x, y []string) bool { return slices.Equal(x, y) })
}

func TestRead_csvTitleLineFirst(t *testing.T) {
	// Над таблицей — название отчета без разделителей (живая проверка #580):
	// разделитель решается по строкам таблицы, а не по первой строке файла.
	path := filepath.Join(t.TempDir(), "товары.csv")
	body := "Товары ниши «Куртки» по дням, пример\nАртикул;Название;01.11.2025\n101;Куртка А, синяя;10\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := tablefile.Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 3 || len(got[1]) != 3 || got[2][1] != "Куртка А, синяя" {
		t.Errorf("rows = %q, want the table split by semicolons", got)
	}
}
