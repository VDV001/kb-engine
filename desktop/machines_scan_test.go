package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/usecase/machines"
)

// scannerFake стоит вместо ssh: out — что вернула машина.
type scannerFake struct {
	out  string
	args []string
}

func (f *scannerFake) Run(_ context.Context, args []string, _ string) (string, error) {
	f.args = args
	return f.out, nil
}

// Скан — только в открытом хранилище; находки приходят с пометкой «новое»;
// план (что выполнится) тот же, что показывает сервис.
func TestMachineDesk_scan(t *testing.T) {
	d, _, _ := newMachineDesk(t)
	fake := &scannerFake{out: "##srv\nпроект-а\nd\n"}
	d.scanner = fake
	if _, err := d.MachineScan("build-01"); !errors.Is(err, errVaultClosed) {
		t.Fatalf("скан при закрытом хранилище: %v", err)
	}
	if err := d.Open(); err != nil {
		t.Fatal(err)
	}
	in := buildIn()
	in.Auth = "key"
	if err := d.AddMachine(in, ""); err != nil {
		t.Fatal(err)
	}
	found, err := d.MachineScan("build-01")
	if err != nil || len(found) != 2 {
		t.Fatalf("находки %+v, %v", found, err)
	}
	if found[0].Name != "проект-а" || found[0].New || found[1].Name != "d" || !found[1].New {
		t.Fatalf("пометки «новое»: %+v", found)
	}
	if !slices.Equal(fake.args, []string{"-p", "22", "deploy@10.0.0.5"}) {
		t.Fatalf("аргументы ssh: %q", fake.args)
	}
	if !slices.Equal(d.MachineScanPlan(), machines.ScanPlan()) {
		t.Fatal("план скана в окне расходится с сервисом")
	}
}

// ssh запускается без вопросов (BatchMode) и с таймаутом соединения; команда на
// машине идет последним аргументом. Ошибка ssh — его сообщение, без вывода машины.
func TestSSHRunner(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "ssh-ok")
	if err := os.WriteFile(ok, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := sshRunner{bin: ok}.Run(context.Background(), []string{"-p", "22", "ops@h"}, "REMOTE")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if lines[len(lines)-1] != "REMOTE" || !slices.Contains(lines, "BatchMode=yes") || !slices.Contains(lines, "ConnectTimeout=10") {
		t.Fatalf("аргументы ssh: %q", lines)
	}
	if slices.Index(lines, "BatchMode=yes") > slices.Index(lines, "ops@h") {
		t.Fatalf("опции после адреса: %q", lines)
	}

	bad := filepath.Join(dir, "ssh-bad")
	script := "#!/bin/sh\necho секрет-в-выводе\necho 'Host key verification failed.' >&2\nexit 255\n"
	if err := os.WriteFile(bad, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = sshRunner{bin: bad}.Run(context.Background(), []string{"ops@h"}, "REMOTE")
	if err == nil || !strings.Contains(err.Error(), "Host key verification failed") || strings.Contains(err.Error(), "секрет-в-выводе") {
		t.Fatalf("ошибка ssh: %v", err)
	}
}
