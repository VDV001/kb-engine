package main

import (
	"context"
	"log"
	"net"
	"net/url"
	"os/exec"
	"sync"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/desktopconfig"
)

// Сетевая папка на постоянной основе (решение владельца 06.11): у папки есть
// адрес шары, и когда путь пропал (сон, перезагрузка, Wi-Fi), окно подключает
// шару системным mount volume — пароль берет Связка ключей macOS, как у
// Finder. Ставить ничего не нужно.

// shareRetry — не чаще этого пробовать одну шару: недоступная не дергается
// на каждом тике.
const shareRetry = 5 * time.Minute

// mountTimeout — сколько ждать подключение (диалог пароля при первом входе).
const mountTimeout = 2 * time.Minute

// shareTries remembers when each share was last tried.
type shareTries struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *shareTries) due(addr string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[string]time.Time{}
	}
	if now.Sub(t.last[addr]) < shareRetry {
		return false
	}
	t.last[addr] = now
	return true
}

// mountCommand is the system command that mounts addr. Адрес проверен
// ShareVolume: кавычек и обратной черты в нем нет.
func mountCommand(addr string) *exec.Cmd {
	return exec.Command("/usr/bin/osascript", "-e", `mount volume "`+addr+`"`)
}

func mountShareCmd(addr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), mountTimeout)
	defer cancel()
	cmd := mountCommand(addr)
	run := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	return run.Run()
}

// shareReachable reports whether the share's server answers on its port:
// SMB 445, AFP 548, WebDAV по https 443.
func shareReachable(addr string) bool {
	u, err := url.Parse(addr)
	if err != nil || u.Hostname() == "" {
		return false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"smb": "445", "afp": "548", "https": "443"}[u.Scheme]
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(u.Hostname(), port), 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// mountMissingShares starts mounting the enabled network folders whose path
// is gone; f.mu held. Подключение идет в фоне: следующий тик reconcile
// откроет вернувшуюся папку.
func (f *folderSettings) mountMissingShares() {
	if f.mountShare == nil {
		return
	}
	now := time.Now()
	for _, s := range f.cfg.WithSources().Sources {
		if s.Kind != desktopconfig.KindFolder || !s.Enabled || s.URL == "" || isDir(desktopconfig.ExpandHome(s.Path, f.home)) {
			continue
		}
		if !f.shares.due(s.URL, now) {
			continue
		}
		up := f.shareUp
		if up == nil {
			up = shareReachable
		}
		go func(addr string) {
			if !up(addr) {
				return // сервер не отвечает — без попытки и без диалога macOS
			}
			if err := f.mountShare(addr); err != nil {
				log.Printf("files: сетевая папка %s не подключилась: %v", addr, err)
			}
		}(s.URL)
	}
}
