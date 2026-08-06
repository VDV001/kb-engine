package xlsxdimtest

import (
	"os"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// A check whose failure mode is silence has to be shown failing. These cases
// plant exactly what the check exists to catch and demand it complains, then
// plant a healthy sheet and demand it stays quiet — otherwise "no complaints"
// would mean the same thing as "not looking".
func TestComplain(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sheet string
		want  string // substring the complaint must carry; "" means silence
	}{
		{
			name:  "declaration covers the cells",
			sheet: `<worksheet><dimension ref="A1:C5"/><sheetData><row r="5"><c r="C5"><v>1</v></c></row></sheetData></worksheet>`,
		},
		{
			name:  "declaration wider than the content is fine",
			sheet: `<worksheet><dimension ref="A1:Z100"/><sheetData><row r="2"><c r="B2"><v>1</v></c></row></sheetData></worksheet>`,
		},
		{
			name:  "rows past the declaration",
			sheet: `<worksheet><dimension ref="A1:C7"/><sheetData><row r="8"><c r="A8"><v>1</v></c></row></sheetData></worksheet>`,
			want:  "declares A1:C7",
		},
		{
			name:  "columns past the declaration",
			sheet: `<worksheet><dimension ref="A1:B9"/><sheetData><row r="2"><c r="H2"><v>1</v></c></row></sheetData></worksheet>`,
			want:  "column 8",
		},
		{
			name:  "single-cell declaration, which is what excelize writes for a new file",
			sheet: `<worksheet><dimension ref="A1"/><sheetData><row r="3"><c r="D3"><v>1</v></c></row></sheetData></worksheet>`,
			want:  "declares A1 ",
		},
		{
			name:  "no declaration at all",
			sheet: `<worksheet><sheetData><row r="3"><c r="A3"><v>1</v></c></row></sheetData></worksheet>`,
			want:  "no <dimension> at all",
		},
		{
			name:  "an empty sheet has nothing to declare",
			sheet: `<worksheet><dimension ref="A1"/><sheetData/></worksheet>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := complain(t, "sheet.xml", tc.sheet)
			switch {
			case tc.want == "" && got != "":
				t.Errorf("complained about a healthy sheet: %s", got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Errorf("complaint = %q, want it to mention %q", got, tc.want)
			}
		})
	}
}

// The same check over a real file, so the zip walking is exercised too and not
// only the string handling underneath it.
func TestFile(t *testing.T) {
	path := t.TempDir() + "/book.xlsx"
	f := excelize.NewFile()
	if err := f.SetCellStr("Sheet1", "C4", "хвост"); err != nil {
		t.Fatalf("set cell: %v", err)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// excelize leaves the declaration at A1 — the defect this package exists
	// for. Seeing it here is the proof the check is wired to a real file.
	if got := File(t, path); len(got) == 0 {
		t.Fatal("no complaint about a file excelize left declaring A1")
	}

	fixed, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := fixed.SetSheetDimension("Sheet1", "A1:C4"); err != nil {
		t.Fatalf("set dimension: %v", err)
	}
	if err := fixed.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := fixed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := File(t, path); len(got) != 0 {
		t.Errorf("still complaining after the declaration was fixed: %v", got)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := Bytes(t, body); len(got) != 0 {
		t.Errorf("Bytes disagrees with File: %v", got)
	}
}
