package main

import (
	"sync"
	"testing"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Сетевая папка пропала (сон, перезагрузка) — окно подключает шару само,
// не чаще раза в shareRetry; смонтированную и без адреса не трогает.
func TestReconcile_MountsShare(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	var mu sync.Mutex
	var mounted []string
	s.mountShare = func(addr string) error {
		mu.Lock()
		mounted = append(mounted, addr)
		mu.Unlock()
		return nil
	}
	_ = s.updateConfig(func(c *desktopconfig.Config) {
		c.Sources = append(c.WithSources().Sources,
			desktopconfig.Source{ID: "sh1", Kind: desktopconfig.KindFolder, Path: "/Volumes/нет-такой-шары-kb", URL: "smb://nas.local/нет-такой-шары-kb", Enabled: true},
			desktopconfig.Source{ID: "sh2", Kind: desktopconfig.KindFolder, Path: "/Volumes/off-kb", URL: "smb://nas.local/off-kb", Enabled: false})
	})
	s.reconcile()
	s.reconcile() // второй тик сразу — без повторного подключения
	waitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(mounted) >= 1 })
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(mounted) != 1 || mounted[0] != "smb://nas.local/нет-такой-шары-kb" {
		t.Fatalf("mounted = %v", mounted)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("не дождался")
}

// Команда подключения: системный mount volume с адресом в кавычках AppleScript.
func TestMountCommand(t *testing.T) {
	cmd := mountCommand("smb://nas.local/docs")
	if len(cmd.Args) != 3 || cmd.Args[0] != "/usr/bin/osascript" || cmd.Args[2] != `mount volume "smb://nas.local/docs"` {
		t.Fatalf("args = %q", cmd.Args)
	}
}
