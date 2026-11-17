package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// applyBuild is what Install does with a verified build (#651): на Mac — подмена
// бандла и перезапуск окна, на Windows — запуск установщика (он ставит поверх
// запущенного окна), иначе проверенный файл кладется в «Загрузки».
func applyBuild(app *App, home string) func(context.Context, string, string) error {
	return func(_ context.Context, path, version string) error {
		exe, _ := os.Executable()
		exe, _ = filepath.EvalSymlinks(exe)
		switch goruntime.GOOS {
		case "darwin":
			if bundle, ok := bundleOf(exe); ok {
				if err := installMac(path, bundle); err != nil {
					return err
				}
				app.update.noteRestart()
				restartLater(app, exec.Command("/bin/sh", "-c", `sleep 1; /usr/bin/open -n "$0"`, bundle))
				return nil
			}
		case "windows":
			saved, err := saveForManual(home, path, version) // установщику нужен файл, а временный Install стирает
			if err != nil {
				return err
			}
			app.update.noteSaved(saved)
			// ponytail: на настоящем Windows не запускалось (#651); потолок — установщик без прав
			// на каталог окна; путь вверх — запуск от имени пользователя с UAC.
			if err := exec.Command(saved).Start(); err != nil {
				return err
			}
			app.update.noteRestart()
			restartLater(app, nil)
			return nil
		}
		saved, err := saveForManual(home, path, version)
		if err == nil {
			app.update.noteSaved(saved)
		}
		return err
	}
}

// restartLater starts after (если есть) и закрывает окно чуть позже: страница
// успевает получить ответ UpdateInstall.
func restartLater(app *App, after *exec.Cmd) {
	go func() {
		if after != nil {
			_ = after.Start()
		}
		time.Sleep(500 * time.Millisecond)
		wr.Quit(app.ctx)
	}()
}
