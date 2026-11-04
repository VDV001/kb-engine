//go:build unix

package filelock

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Занятый замок до срока ctx — ErrBusy, и fn не зовется: читатель вроде
// MCP-сервера не должен висеть, пока окно ждет Связку ключей.
func TestWithContext_BusyUntilDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	err := With(path, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		called := false
		err := WithContext(ctx, path, func() error { called = true; return nil })
		if called {
			t.Error("fn вызвана при занятом замке")
		}
		return err
	})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// Замок, отпущенный до срока, берется: ожидание, а не отказ с первой попытки.
func TestWithContext_WaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = With(path, func() error { close(held); <-release; return nil })
	}()
	<-held
	time.AfterFunc(30*time.Millisecond, func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	called := false
	if err := WithContext(ctx, path, func() error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("fn не вызвана")
	}
}

// Ошибка fn возвращается как есть, а замок после нее снят.
func TestWithContext_ReturnsFnErrorAndUnlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	boom := errors.New("boom")
	if err := WithContext(context.Background(), path, func() error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := WithContext(ctx, path, func() error { return nil }); err != nil {
		t.Fatalf("замок не снят: %v", err)
	}
}

// Отмена вызывающим — не «занято»: агент, отменивший запрос, не должен
// слышать «повторите поиск».
func TestWithContext_CancelIsNotBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	err := With(path, func() error {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(30*time.Millisecond, cancel)
		return WithContext(ctx, path, func() error { return nil })
	})
	if errors.Is(err, ErrBusy) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled без ErrBusy", err)
	}
}
