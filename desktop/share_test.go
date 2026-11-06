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
	s.shareUp = func(string) bool { return true }
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

// Сервер шары не отвечает (ноутбук вне домашней сети) — окно не зовет
// mount volume: иначе macOS показывал бы диалог ошибки на каждом тике (ревью #469).
func TestReconcile_SkipsUnreachableShare(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	called := make(chan string, 1)
	s.shareUp = func(string) bool { return false }
	s.mountShare = func(addr string) error { called <- addr; return nil }
	_ = s.updateConfig(func(c *desktopconfig.Config) {
		c.Sources = append(c.WithSources().Sources,
			desktopconfig.Source{ID: "sh1", Kind: desktopconfig.KindFolder, Path: "/Volumes/нет-такой-шары-kb", URL: "smb://nas.local/нет-такой-шары-kb", Enabled: true})
	})
	s.reconcile()
	select {
	case a := <-called:
		t.Fatalf("недоступная шара подключалась: %s", a)
	case <-time.After(200 * time.Millisecond):
	}
}

// Где подключена шара — по ее серверу и имени среди подключенных: занятое имя
// macOS подключает в /Volumes/имя-1, и путь /Volumes/имя вел бы в чужую папку
// (ревью #469).
func TestMountPointOf(t *testing.T) {
	mounts := []mountEntry{
		{from: "/dev/disk3s1s1", on: "/"},
		{from: "//person@other.local/team", on: "/Volumes/team"},
		{from: "//person@nas.local/team", on: "/Volumes/team-1"},
		{from: "//nas.local/%D0%B4%D0%BE%D0%BA%D0%B8", on: "/Volumes/доки"},
		{from: "https://webdav.yandex.ru/", on: "/Volumes/webdav.yandex.ru"},
	}
	cases := []struct{ addr, want string }{
		{"smb://nas.local/team", "/Volumes/team-1"},
		{"smb://NAS.local/Team/sub", "/Volumes/team-1"},
		{"smb://nas.local/доки", "/Volumes/доки"},
		{"https://webdav.yandex.ru", "/Volumes/webdav.yandex.ru"},
		{"smb://nas.local/нет", ""},
		{"smb://third.local/team", ""},
	}
	for _, tc := range cases {
		if got := mountPointOf(tc.addr, mounts); got != tc.want {
			t.Errorf("%s → %q, want %q", tc.addr, got, tc.want)
		}
	}
}

// Проверка, есть ли папка шары, идет не под замком настроек: отвалившаяся
// сетевая папка отвечает на stat минутами, и окно стояло бы (ревью #469).
func TestReconcile_DirCheckOutsideLock(t *testing.T) {
	s, _, _ := newSourcesSettings(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.dirExists = func(p string) bool {
		if p == "/Volumes/висит-kb" {
			<-release
		}
		return isDir(p)
	}
	s.shareUp = func(string) bool { return false }
	s.mountShare = func(string) error { return nil }
	_ = s.updateConfig(func(c *desktopconfig.Config) {
		c.Sources = append(c.WithSources().Sources,
			desktopconfig.Source{ID: "sh1", Kind: desktopconfig.KindFolder, Path: "/Volumes/висит-kb", URL: "smb://nas.local/висит-kb", Enabled: true})
	})
	done := make(chan struct{})
	go func() { s.reconcile(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile ждет stat сетевой папки под замком")
	}
}
