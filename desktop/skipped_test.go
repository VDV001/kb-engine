package main

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/filesearch"
)

// «Подробнее» у «N не прочитано»: статус окна несет файлы с причиной словами
// человека и исходной ошибкой — по ней видно, что именно сломалось.
func TestSkippedRows(t *testing.T) {
	err := fmt.Errorf("open: %w", fs.ErrPermission)
	rows := skippedRows([]filesearch.SkippedFile{{Path: "/w/закрыто.md", Err: err}, {Path: "/w/x.md", Err: errors.New("boom")}})
	if len(rows) != 2 || rows[0].Path != "/w/закрыто.md" || rows[0].Detail != err.Error() {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Reason == "" || rows[0].Reason == rows[1].Reason {
		t.Errorf("причины %q и %q: у прав своя причина", rows[0].Reason, rows[1].Reason)
	}
}
