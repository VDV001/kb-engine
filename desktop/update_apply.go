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
				app.update.noteRestart(version)
				service := os.Getenv("XPC_SERVICE_NAME")
				restartLater(app, restartCommand(service, os.Getuid(), bundle, os.Getpid()), service == launchdLabel)
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
			app.update.noteRestart(version)
			restartLater(app, nil, false)
			return nil
		}
		saved, err := saveForManual(home, path, version)
		if err == nil {
			app.update.noteSaved(saved)
		}
		return err
	}
}

// restartLater restarts the window a moment later: человек успевает увидеть
// шаг «Перезапускаюсь». Под launchd (launchd) команда сама перезапускает службу
// и убивает это окно; иначе помощник стартует, окно закрывается.
func restartLater(app *App, cmd *exec.Cmd, launchd bool) {
	go func() {
		time.Sleep(1500 * time.Millisecond)
		switch {
		case cmd != nil && launchd:
			_ = cmd.Run()
		case cmd != nil:
			_ = cmd.Start()
		}
		time.Sleep(500 * time.Millisecond)
		wr.Quit(app.ctx)
	}()
}
