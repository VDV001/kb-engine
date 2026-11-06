package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Диктовка (#436): помощник kb-listen рядом с окном слушает микрофон через
// Apple Speech и отдает текст по мере речи строками JSON; окно пересылает их
// странице событием dictation. Стоп — закрытый stdin: помощник дослушивает и
// отдает итог. Звук никуда не пишется.

// maxDictation caps one dictation: забытая включенной диктовка не слушает часами.
const maxDictation = 5 * time.Minute

// stopGrace — сколько ждать итога после стопа, прежде чем остановить помощника.
const stopGrace = 5 * time.Second

// listenEvent is one line of kb-listen; Final — диктовка кончилась.
type listenEvent struct {
	Ready    bool   `json:"ready,omitempty"`
	OnDevice bool   `json:"onDevice,omitempty"`
	Text     string `json:"text,omitempty"`
	Final    bool   `json:"final,omitempty"`
	Error    string `json:"error,omitempty"`
}

// dictation runs one kb-listen at a time.
type dictation struct {
	bin  string
	emit func(listenEvent)

	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
}

// listenNextTo finds kb-listen next to the window's binary.
func listenNextTo(exe string) string {
	bin := filepath.Join(filepath.Dir(exe), "kb-listen")
	if info, err := os.Stat(bin); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return bin
	}
	return ""
}

// Start begins listening; pause > 0 — разговор без рук: тишина pause секунд
// после речи кончает вопрос. Вторая диктовка поверх идущей — отказ.
func (d *dictation) Start(pause float64) error {
	if d.bin == "" {
		return errors.New("диктовка работает только в собранном окне: нет помощника kb-listen")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cmd != nil {
		return errors.New("диктовка уже идет")
	}
	args := []string{"--lang", "ru-RU"}
	if pause > 0 {
		args = append(args, "--pause", strconv.FormatFloat(pause, 'f', 1, 64))
	}
	cmd := exec.Command(d.bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	d.cmd, d.stdin = cmd, stdin
	limit := time.AfterFunc(maxDictation, d.Stop)
	go d.read(cmd, out, limit)
	return nil
}

// read forwards the helper's lines and reports the end once, with the last text.
func (d *dictation) read(cmd *exec.Cmd, out io.Reader, limit *time.Timer) {
	sc := bufio.NewScanner(out)
	var last *listenEvent // итог придерживается до освобождения помощника
	for sc.Scan() {
		var ev listenEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue // строка не по протоколу — не текст
		}
		if ev.Final {
			last = &ev
			continue
		}
		d.emit(ev)
	}
	_ = cmd.Wait() // код выхода уже сказан строкой error
	limit.Stop()
	d.mu.Lock()
	if d.cmd == cmd {
		d.cmd, d.stdin = nil, nil
	}
	d.mu.Unlock()
	// Итог — после освобождения: страница, что сразу слушает снова (разговор
	// голосом), не упирается в «диктовка уже идет» (ревью #465).
	if last == nil {
		last = &listenEvent{Final: true}
	}
	d.emit(*last)
}

// Stop ends the dictation: помощник дослушивает и отдает итог; не успел за
// stopGrace — останавливается.
func (d *dictation) Stop() {
	d.mu.Lock()
	cmd, stdin := d.cmd, d.stdin
	d.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = stdin.Close()
	time.AfterFunc(stopGrace, func() {
		d.mu.Lock()
		running := d.cmd == cmd
		d.mu.Unlock()
		if running {
			_ = cmd.Process.Kill()
		}
	})
}

// listenStatus is what the settings say about dictation: разрешение, без сети ли.
type listenStatus struct {
	Helper    bool   `json:"helper"`
	Auth      string `json:"auth,omitempty"` // authorized | denied | restricted | notDetermined
	OnDevice  bool   `json:"onDevice"`
	Available bool   `json:"available"`
}

// Check asks kb-listen --check: разрешений не запрашивает.
func (d *dictation) Check() listenStatus {
	if d.bin == "" {
		return listenStatus{}
	}
	out, err := exec.Command(d.bin, "--check").Output()
	st := listenStatus{Helper: true}
	if err == nil {
		_ = json.Unmarshal(out, &st)
		st.Helper = true
	}
	return st
}
