package main

import (
	"bufio"
	"context"
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

// transcribeTimeout — сколько ждать сервер распознавания.
const transcribeTimeout = 2 * time.Minute

// listenEvent is one line of kb-listen; Final — диктовка кончилась.
type listenEvent struct {
	Ready    bool   `json:"ready,omitempty"`
	OnDevice bool   `json:"onDevice,omitempty"`
	Text     string `json:"text,omitempty"`
	Final    bool   `json:"final,omitempty"`
	Error    string `json:"error,omitempty"`
	// Busy — запись отдана серверу распознавания, ждем текст.
	Busy bool `json:"busy,omitempty"`
	// Recorded — путь записи от помощника; странице не уходит.
	Recorded string `json:"-"`
}

// transcriber turns a WAV recording into text (сервер распознавания речи).
type transcriber func(ctx context.Context, wav []byte) (string, error)

// helperEvent is one line of kb-listen as it comes: путь записи — тоже.
type helperEvent struct {
	listenEvent
	Recorded string `json:"recorded,omitempty"`
}

// dictation runs one kb-listen at a time.
type dictation struct {
	bin    string
	emit   func(listenEvent)
	tmpDir string // куда помощник пишет запись; "" — системная временная папка

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

// Start begins listening with Apple Speech; pause > 0 — разговор без рук:
// тишина pause секунд после речи кончает вопрос. Вторая диктовка поверх
// идущей — отказ.
func (d *dictation) Start(pause float64) error { return d.StartWith(pause, nil) }

// StartWith listens with tr when given: помощник пишет речь в WAV, окно
// отдает ее серверу распознавания; nil — Apple Speech.
func (d *dictation) StartWith(pause float64, tr transcriber) error {
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
	if tr != nil {
		f, err := os.CreateTemp(d.tmpDir, "kb-listen-*.wav")
		if err != nil {
			return err
		}
		_ = f.Close()
		args = append(args, "--record", f.Name())
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
	go d.read(cmd, out, limit, tr)
	return nil
}

// read forwards the helper's lines and reports the end once, with the last text.
func (d *dictation) read(cmd *exec.Cmd, out io.Reader, limit *time.Timer, tr transcriber) {
	sc := bufio.NewScanner(out)
	final := false
	for sc.Scan() {
		var he helperEvent
		if json.Unmarshal(sc.Bytes(), &he) != nil {
			continue // строка не по протоколу — не текст
		}
		ev := he.listenEvent
		if he.Recorded != "" {
			ev = d.transcribe(he.Recorded, tr)
		}
		final = final || ev.Final
		d.emit(ev)
	}
	_ = cmd.Wait() // код выхода уже сказан строкой error
	limit.Stop()
	d.mu.Lock()
	if d.cmd == cmd {
		d.cmd, d.stdin = nil, nil
	}
	d.mu.Unlock()
	if !final {
		d.emit(listenEvent{Final: true})
	}
}

// transcribe sends the recording to the speech server and deletes it.
func (d *dictation) transcribe(path string, tr transcriber) listenEvent {
	defer func() { _ = os.Remove(path) }()
	if tr == nil {
		return listenEvent{Final: true, Error: "запись без сервера распознавания"}
	}
	d.emit(listenEvent{Busy: true})
	wav, err := os.ReadFile(path)
	if err != nil {
		return listenEvent{Final: true, Error: "запись не прочитана: " + err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), transcribeTimeout)
	defer cancel()
	text, err := tr(ctx, wav)
	if err != nil {
		return listenEvent{Final: true, Error: err.Error()}
	}
	return listenEvent{Text: text, Final: true}
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
