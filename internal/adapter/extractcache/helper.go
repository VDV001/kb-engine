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

// ErrTimeout is returned when the helper ran past its Timeout: это не «файл
// битый», распознавание стоит повторить на следующем проходе.
var ErrTimeout = errors.New("kb-extract timed out")

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

// Extract returns the text of the file read from r — ext is its kind, "pdf"
// or an image extension — cut to MaxText on a rune boundary. Файл приходит
// содержимым в stdin, а не путем: помощник ничего не открывает сам, и граница
// os.Root, через которую файл открыл вызывающий, не обходится (ревью #379).
// Вывод сверх предела не читается: процесс останавливается.
func (h Helper) Extract(ctx context.Context, r io.Reader, ext string) (string, error) {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = helperTimeout
	}
	run, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(run, h.Bin, "-", ext)
	cmd.Stdin = r
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
	// Таймаут — первым: после него WaitDelay закрывает трубу, и чтение
	// заканчивается ошибкой «file already closed», а не EOF (так на Linux в CI,
	// на macOS успевал EOF).
	case errors.Is(run.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return "", fmt.Errorf("%w after %s", ErrTimeout, timeout)
	case rerr != nil:
		return "", fmt.Errorf("kb-extract: %w", rerr)
	case full:
	case errors.As(werr, &exit) && exit.ExitCode() == 2:
		return "", ErrUnreadable
	case werr != nil:
		return "", fmt.Errorf("kb-extract: %w: %s", werr, strings.TrimSpace(stderr.String()))
	}
	// ToValidUTF8 убирает и руну, разрезанную пределом: отдельная обрезка
	// хвоста срезала целые символы («Итого: 5» → «Итого: », ревью #379).
	return strings.ToValidUTF8(string(b), ""), nil
}
