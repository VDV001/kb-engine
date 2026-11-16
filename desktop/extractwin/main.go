// kb-extract.exe - <pdf|png|jpg|…> — текст PDF или картинки для поиска окна DVK
// на Windows (#622, этап 5): тот же протокол, что у desktop/extract/main.swift
// на Маке. Файл приходит содержимым в stdin — помощник ничего не открывает сам
// и не уходит по ссылке за пределы папок поиска; на время распознавания он
// лежит во временной копии, которая стирается. Читает Windows: ocr.ps1
// (Windows.Data.Pdf + Windows.Media.Ocr), ставить ничего не нужно.
//
// ponytail: PDF с текстовым слоем тоже распознается по картинке страниц —
// у Windows нет системного извлечения текста PDF. Потолок: медленнее Мака и
// ошибки OCR в «живом» тексте, 30 страниц. Путь: свой разбор потоков текста
// PDF на Go и OCR только для сканов.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

//go:embed ocr.ps1
var script string

// staleAfter — копия старше уже не чья-то текущая: помощник живет не дольше
// срока окна (2 мин).
const staleAfter = time.Hour

// sweepStale removes copies left by helpers killed before their defer ran:
// снятый по сроку помощник не стирает свою копию личного документа (ревью #637).
func sweepStale(dir string, now time.Time) {
	old, _ := filepath.Glob(filepath.Join(dir, "kb-extract-*"))
	for _, p := range old {
		if fi, err := os.Stat(p); err == nil && now.Sub(fi.ModTime()) > staleAfter {
			_ = os.Remove(p)
		}
	}
}

// maxInput — больше файл не копируется: окно таких и не шлет.
const maxInput = 256 << 20

var extOK = regexp.MustCompile(`^[a-z0-9]{1,5}$`)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 2 || args[0] != "-" || !extOK.MatchString(args[1]) {
		fmt.Fprintln(errOut, "usage: kb-extract - <pdf|png|jpg|…>")
		return 2
	}
	dieTogether()
	sweepStale(os.TempDir(), time.Now())
	f, err := os.CreateTemp("", "kb-extract-*."+args[1])
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 2
	}
	defer os.Remove(f.Name()) //nolint:errcheck
	n, err := io.Copy(f, io.LimitReader(in, maxInput+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || n > maxInput {
		fmt.Fprintln(errOut, "файл не прочитан или больше 256 МБ")
		return 2
	}
	cmd := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.Env = append(os.Environ(), "KB_FILE="+f.Name(), "KB_EXT="+args[1])
	hideWindow(cmd)
	cmd.Stdout, cmd.Stderr = out, errOut
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			return exit.ExitCode()
		}
		fmt.Fprintln(errOut, err)
		return 2
	}
	return 0
}

// powershell — системный, по полному пути, а не первый в PATH.
func powershell() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}
