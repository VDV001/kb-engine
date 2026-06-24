package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_auditAgeRecognized(t *testing.T) {
	path := writeCatalog(t, `{"entries":[
		{"id":1,"habr_id":1,"title":"T","url":"https://habr.com/ru/articles/1/","category":"golang","status":"keep","date_created":"2020-01-01"}
	]}`)
	var out, errb bytes.Buffer
	if code := run([]string{"audit", "--catalog", path, "--check", "age"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errb.String())
	}
	if strings.Contains(errb.String(), "unknown --check") {
		t.Errorf("age check not recognized: %s", errb.String())
	}
	// The 2020 article is well over 18 months old → must be reported.
	if !strings.Contains(out.String(), "id=1") {
		t.Errorf("expected old article id=1 in age output:\n%s", out.String())
	}
}
