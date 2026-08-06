// Package xlsxdimtest checks a written workbook the way an outside reader
// sees it: through the range each sheet declares, not through excelize.
//
// It lives apart from the tests that use it because both packages that write
// workbooks need the same check, and a check copied into two test files is one
// edit away from asking two different questions.
package xlsxdimtest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

var (
	dimRe  = regexp.MustCompile(`<dimension ref="([^"]+)"`)
	cellRe = regexp.MustCompile(`<c r="([A-Z]+)(\d+)"`)
)

// File reports every sheet in the workbook at path whose declared range falls
// short of the cells it holds. An empty result means a streaming reader sees
// the whole file.
func File(tb testing.TB, path string) []string {
	tb.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read %s: %v", path, err)
	}
	return Bytes(tb, body)
}

// Bytes is File for a workbook that was never written to disk — the HTTP
// export renders straight into the response.
func Bytes(tb testing.TB, body []byte) []string {
	tb.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		tb.Fatalf("open workbook as zip: %v", err)
	}

	var complaints []string
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			tb.Fatalf("open %s: %v", f.Name, err)
		}
		sheet, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			tb.Fatalf("read %s: %v", f.Name, err)
		}
		if c := complain(tb, f.Name, string(sheet)); c != "" {
			complaints = append(complaints, c)
		}
	}
	return complaints
}

// complain returns the sheet's grievance, or "" when its declaration covers it.
func complain(tb testing.TB, name, sheet string) string {
	tb.Helper()

	var lastRow, lastCol int
	for _, m := range cellRe.FindAllStringSubmatch(sheet, -1) {
		col, err := excelize.ColumnNameToNumber(m[1])
		if err != nil {
			tb.Fatalf("%s: column %q: %v", name, m[1], err)
		}
		row, err := strconv.Atoi(m[2])
		if err != nil {
			tb.Fatalf("%s: row %q: %v", name, m[2], err)
		}
		lastCol, lastRow = max(lastCol, col), max(lastRow, row)
	}
	if lastRow == 0 {
		return "" // an empty sheet declares nothing worth checking
	}

	decl := dimRe.FindStringSubmatch(sheet)
	if decl == nil {
		return name + ": no <dimension> at all"
	}
	ref := decl[1]
	if _, end, found := strings.Cut(ref, ":"); found {
		ref = end
	}
	col, row, err := excelize.CellNameToCoordinates(ref)
	if err != nil {
		tb.Fatalf("%s: dimension %q: %v", name, decl[1], err)
	}
	if row < lastRow || col < lastCol {
		return fmt.Sprintf("%s: declares %s but holds cells out to row %d, column %d",
			name, decl[1], lastRow, lastCol)
	}
	return ""
}
