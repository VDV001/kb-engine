package xlsxdim_test

import (
	"os"
	"testing"

	"github.com/daniil/kb-engine/internal/adapter/xlsxdim"
	"github.com/daniil/kb-engine/internal/adapter/xlsxdim/xlsxdimtest"
	"github.com/xuri/excelize/v2"
)

// saved renders the file to bytes the way both writers eventually do.
func saved(t *testing.T, f *excelize.File) []byte {
	t.Helper()
	path := t.TempDir() + "/book.xlsx"
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	return body
}

func TestSync(t *testing.T) {
	for _, tc := range []struct {
		name string
		// build returns a workbook and the range its sheet should end up
		// declaring. "" means the sheet holds nothing and is left alone.
		build func(t *testing.T) (*excelize.File, string)
	}{
		{
			name: "a fresh file declares what it holds, not A1",
			build: func(t *testing.T) (*excelize.File, string) {
				f := excelize.NewFile()
				set(t, f, "Sheet1", "A1", "заголовок")
				set(t, f, "Sheet1", "C5", "хвост")
				return f, "A1:C5"
			},
		},
		{
			name: "a declaration wider than the content survives",
			build: func(t *testing.T) (*excelize.File, string) {
				f := excelize.NewFile()
				set(t, f, "Sheet1", "B2", "одна ячейка")
				if err := f.SetSheetDimension("Sheet1", "A1:Z100"); err != nil {
					t.Fatalf("set dimension: %v", err)
				}
				return f, "A1:Z100"
			},
		},
		{
			name: "each axis is widened on its own",
			build: func(t *testing.T) (*excelize.File, string) {
				f := excelize.NewFile()
				set(t, f, "Sheet1", "D2", "правее объявленного")
				if err := f.SetSheetDimension("Sheet1", "A1:B40"); err != nil {
					t.Fatalf("set dimension: %v", err)
				}
				return f, "A1:D40"
			},
		},
		{
			name: "every sheet is covered, not just the first",
			build: func(t *testing.T) (*excelize.File, string) {
				f := excelize.NewFile()
				set(t, f, "Sheet1", "A1", "первый")
				if _, err := f.NewSheet("Второй"); err != nil {
					t.Fatalf("new sheet: %v", err)
				}
				set(t, f, "Второй", "E9", "второй")
				return f, "A1:E9"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, want := tc.build(t)
			defer func() { _ = f.Close() }()

			if err := xlsxdim.Sync(f); err != nil {
				t.Fatalf("Sync: %v", err)
			}

			sheet := f.GetSheetList()[len(f.GetSheetList())-1]
			got, err := f.GetSheetDimension(sheet)
			if err != nil {
				t.Fatalf("GetSheetDimension: %v", err)
			}
			if got != want {
				t.Errorf("sheet %q declares %q, want %q", sheet, got, want)
			}
			if c := xlsxdimtest.Bytes(t, saved(t, f)); len(c) != 0 {
				t.Errorf("saved file still short-changes a streaming reader: %v", c)
			}
		})
	}
}

// An empty sheet is left as it is: there is no range to declare, and inventing
// one would claim cells that do not exist.
func TestSync_leavesAnEmptySheetAlone(t *testing.T) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	before, err := f.GetSheetDimension("Sheet1")
	if err != nil {
		t.Fatalf("GetSheetDimension: %v", err)
	}
	if err := xlsxdim.Sync(f); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	after, err := f.GetSheetDimension("Sheet1")
	if err != nil {
		t.Fatalf("GetSheetDimension: %v", err)
	}
	if before != after {
		t.Errorf("empty sheet went from %q to %q", before, after)
	}
}

func set(t *testing.T, f *excelize.File, sheet, cell, value string) {
	t.Helper()
	if err := f.SetCellStr(sheet, cell, value); err != nil {
		t.Fatalf("set %s!%s: %v", sheet, cell, err)
	}
}
