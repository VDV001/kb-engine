package extractcache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxText — сколько текста берется от помощника: тот же предел, что у текста
// файла в индексе (localfiles.MaxFileBytes).
const MaxText = 2 << 20

// helperTimeout — дольше помощник на одном файле не работает: скан в 30
// страниц распознается за десятки секунд, зависание — дольше.
const helperTimeout = 2 * time.Minute

// ErrUnreadable is returned when the helper could not open the file (код 2):
// битый, зашифрованный или не тот формат.
var ErrUnreadable = errors.New("file not readable by kb-extract")

// Helper runs kb-extract, the macOS helper on PDFKit and Vision, for one file.
type Helper struct {
	Bin     string
	Timeout time.Duration // ноль — helperTimeout
}

// HelperNextTo finds kb-extract beside the executable exe (kb.app/Contents/MacOS).
func HelperNextTo(exe string) (Helper, bool) {
	bin := filepath.Join(filepath.Dir(exe), "kb-extract")
	info, err := os.Stat(bin)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return Helper{}, false
	}
	return Helper{Bin: bin}, true
}

// Extract returns the text of the file at path, cut to MaxText on a rune
// boundary. Вывод сверх предела не читается: процесс останавливается.
func (h Helper) Extract(ctx context.Context, path string) (string, error) {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = helperTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Bin, path)
	// После отмены трубы закрываются через секунду, даже если их держит
	// потомок процесса: иначе Wait ждал бы копирования stderr вечно.
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("kb-extract: %w", err)
	}
	b, rerr := io.ReadAll(io.LimitReader(out, MaxText))
	full := len(b) == MaxText
	if full {
		cancel() // остальной текст не нужен
	}
	werr := cmd.Wait()
	var exit *exec.ExitError
	switch {
	case rerr != nil:
		return "", fmt.Errorf("kb-extract: %w", rerr)
	case full:
	case errors.As(werr, &exit) && exit.ExitCode() == 2:
		return "", fmt.Errorf("%w: %s", ErrUnreadable, filepath.Base(path))
	case werr != nil:
		return "", fmt.Errorf("kb-extract %s: %w: %s", filepath.Base(path), werr, strings.TrimSpace(stderr.String()))
	}
	return capRunes(strings.ToValidUTF8(string(b), "")), nil
}

// capRunes drops a rune cut in half at the end of s.
func capRunes(s string) string {
	for len(s) > 0 && !utf8.ValidString(s[max(0, len(s)-utf8.UTFMax):]) {
		s = s[:len(s)-1]
	}
	return s
}
