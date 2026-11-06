package main

import (
	"context"
	"log"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"syscall"
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
// is gone; f.mu held. Проверка папки и подключение идут в фоне: stat
// отвалившейся сетевой папки отвечает минутами и держал бы замок настроек
// (ревью #469); следующий тик reconcile откроет вернувшуюся папку.
func (f *folderSettings) mountMissingShares() {
	if f.mountShare == nil {
		return
	}
	now := time.Now()
	up, exists, mount := f.shareUp, f.dirExists, f.mountShare
	if up == nil {
		up = shareReachable
	}
	if exists == nil {
		exists = isDir
	}
	for _, s := range f.cfg.WithSources().Sources {
		if s.Kind != desktopconfig.KindFolder || !s.Enabled || s.URL == "" {
			continue
		}
		go func(path, addr string) {
			if exists(path) || !f.shares.due(addr, now) {
				return
			}
			if !up(addr) {
				return // сервер не отвечает — без попытки и без диалога macOS
			}
			if err := mount(addr); err != nil {
				log.Printf("files: сетевая папка %s не подключилась: %v", addr, err)
			}
		}(desktopconfig.ExpandHome(s.Path, f.home), s.URL)
	}
}

// mountEntry is one mounted file system: откуда и куда.
type mountEntry struct{ from, on string }

// mntNoWait — MNT_NOWAIT из sys/mount.h: список без опроса каждой файловой
// системы (отвалившаяся шара не держит ответ). В syscall для darwin константы нет.
const mntNoWait = 2

// systemMounts lists the mounted file systems (getfsstat).
func systemMounts() []mountEntry {
	n, err := syscall.Getfsstat(nil, mntNoWait)
	if err != nil || n <= 0 {
		return nil
	}
	buf := make([]syscall.Statfs_t, n)
	if n, err = syscall.Getfsstat(buf, mntNoWait); err != nil {
		return nil
	}
	out := make([]mountEntry, 0, n)
	for _, st := range buf[:n] {
		out = append(out, mountEntry{from: cString(st.Mntfromname[:]), on: cString(st.Mntonname[:])})
	}
	return out
}

func cString(b []int8) string {
	s := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		s = append(s, byte(c))
	}
	return string(s)
}

// mountPointOf finds where the share at addr is mounted: том в /Volumes, чей
// источник — тот же сервер и та же папка. Занятое имя macOS подключает в
// /Volumes/имя-1, поэтому путь из адреса угадывать нельзя (ревью #469).
// "" — такой шары среди подключенных нет.
//
// ponytail: вид источника (//user@server/share у SMB, адрес у WebDAV) сверен
// по документации и выводу mount, а не на живой шаре. Потолок: AFP пишет
// сервер с суффиксом службы — принимается префикс «server.». Путь: при
// промахе MountShare берет путь из адреса, если том там не чужой.
func mountPointOf(addr string, mounts []mountEntry) string {
	u, err := url.Parse(addr)
	vol := desktopconfig.ShareVolume(addr)
	if err != nil || vol == "" {
		return ""
	}
	host, name := strings.ToLower(u.Hostname()), strings.TrimPrefix(vol, "/Volumes/")
	for _, m := range mounts {
		if !strings.HasPrefix(m.on, "/Volumes/") {
			continue
		}
		fromHost, fromName := mountSource(m.from)
		if fromName == "" {
			fromName = fromHost
		}
		if strings.EqualFold(fromName, name) && (fromHost == host || strings.HasPrefix(fromHost, host+".")) {
			return m.on
		}
	}
	return ""
}

// mountSource splits a mount source (//user@server/share, https://server/a/b)
// into its server (lower case) and last folder.
func mountSource(from string) (host, name string) {
	if _, rest, ok := strings.Cut(from, "://"); ok {
		from = rest
	} else if rest, ok := strings.CutPrefix(from, "//"); ok {
		from = rest
	} else {
		return "", ""
	}
	h, path, _ := strings.Cut(from, "/")
	if _, after, ok := strings.Cut(h, "@"); ok {
		h = after
	}
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	path = strings.Trim(path, "/")
	name = path[strings.LastIndex(path, "/")+1:]
	if n, err := url.PathUnescape(name); err == nil {
		name = n
	}
	return strings.ToLower(h), name
}

// mountedVolume returns the path of the share just mounted: найденный том,
// иначе путь из адреса, если там не чужой том.
func mountedVolume(addr, vol string, mounts []mountEntry) (string, bool) {
	if p := mountPointOf(addr, mounts); p != "" {
		return p, true
	}
	for _, m := range mounts {
		if m.on == vol {
			return "", false // в /Volumes/имя подключено другое
		}
	}
	return vol, isDir(vol)
}
