package main

import (
	"bytes"
	"strings"
	"testing"
)

// Coverage of links is a different genre from lifecycle candidates: 527 entries
// have never been checked, and listing them one by one would bury the handful of
// findings that need a decision. So --check all does not run it — but it must
// not stay silent either, or the reader concludes the base knows its links are
// fine.
func TestRun_auditAllSummarisesLinkCoverage(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"title":"Never checked","url":"https://h/a","category":"golang","status":"keep"},
		{"id":2,"title":"Checked in May","url":"https://h/b","category":"golang","status":"keep","drift_check_date":"2026-05-16"},
		{"id":3,"title":"No url","url":"","category":"golang","status":"keep"}
	]}`)

	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	s := out.String()
	if !strings.Contains(s, "--check links") {
		t.Errorf("summary does not point at the command that lists them:\n%s", s)
	}
	if !strings.Contains(s, "ни разу") {
		t.Errorf("summary does not mention entries never checked:\n%s", s)
	}
	// One line, not one finding per entry.
	if strings.Count(s, "[links]") != 0 {
		t.Errorf("--check all listed link findings individually:\n%s", s)
	}
}

func TestRun_auditCheckLinksListsThem(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"title":"Never checked","url":"https://h/a","category":"golang","status":"keep"}
	]}`)

	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path, "--check", "links"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "[links] id=1") {
		t.Errorf("explicit --check links did not list the entry:\n%s", out.String())
	}
}
