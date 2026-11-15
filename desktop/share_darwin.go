//go:build darwin

package main

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// Подключение шары и список томов — системные вызовы macOS (share.go о самой
// политике повторов). Под Windows — share_other.go.

// mountTimeout — сколько ждать подключение (диалог пароля при первом входе).
const mountTimeout = 2 * time.Minute

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
