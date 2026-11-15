// DVK-Setup.exe — установщик окна DVK на Windows (#622, этап 5; решение
// владельца 15.11: установщик .exe). Без NSIS и прав администратора: ставит в
// %LOCALAPPDATA%\Programs\DVK, ярлык в «Пуск», автозапуск при входе (окно
// живет в трее и отвечает телефону), запись в «Параметры → Приложения» для
// удаления. Файлы окна вшиты в установщик (payload/, кладет рецепт
// just desktop-windows). Данные окна (%APPDATA%\kb-desktop) и ключи в
// Диспетчере учетных данных удаление не трогает — это данные человека.
package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed all:payload
var payload embed.FS

// files — что ставится; без любого из них сборка установщика неполная.
var files = []string{"kb.exe", "kbengine.exe", "kb-listen.exe", "kb-extract.exe", "dvk.ico"}

const (
	appName   = "DVK"
	publisher = "Daniil Vdovin"
	uninstKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\DVK`
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
)

// version — подставляет рецепт: -ldflags "-X main.version=…".
var version = "dev"

func main() {
	args := os.Args[1:]
	var err error
	switch {
	case slices.Contains(args, "/uninstall"):
		err = uninstall(args)
	default:
		err = install()
	}
	if err != nil && !errors.Is(err, errCancelled) {
		message("DVK", err.Error(), mbError)
		os.Exit(1)
	}
}

var errCancelled = errors.New("отменено")

func installDir() string {
	return filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", appName)
}

func checkPayload() error {
	for _, f := range files {
		if _, err := fs.Stat(payload, "payload/"+f); err != nil {
			return fmt.Errorf("установщик собран без %s — соберите его заново: just desktop-windows", f)
		}
	}
	return nil
}

func install() error {
	if err := checkPayload(); err != nil {
		return err
	}
	dir := installDir()
	note := ""
	if !webView2Installed() {
		note = "\n\nВ Windows нет WebView2 Runtime — без него окно не откроется. Поставьте его с https://go.microsoft.com/fwlink/p/?LinkId=2124703 (бесплатно, от Microsoft)."
	}
	if !confirm("Установка DVK "+version, "Установить DVK в "+dir+"?\n\nDVK будет запускаться при входе в Windows и жить в трее у часов: так окно отвечает телефону. Отключить — Параметры → Приложения → Автозагрузка."+note) {
		return errCancelled
	}
	if err := place(dir); err != nil {
		return err
	}
	exe := filepath.Join(dir, "kb.exe")
	if err := shortcut(startMenuLink(), exe, filepath.Join(dir, "dvk.ico")); err != nil {
		return fmt.Errorf("ярлык в «Пуск» не создан: %w", err)
	}
	if err := register(dir, exe); err != nil {
		return err
	}
	if err := start(exe); err != nil {
		return err
	}
	message("DVK установлен", "Готово. DVK открыт; дальше — ярлык в «Пуск», значок в трее или Ctrl+Alt+Space.", mbInfo)
	return nil
}

// place writes the window's files and the uninstaller (копия установщика) into dir.
func place(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		b, err := payload.ReadFile("payload/" + f)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o755); err != nil {
			return fmt.Errorf("не записал %s: %w — если DVK запущен, закройте его (значок в трее → Выйти) и запустите установку снова", f, err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return copyFile(self, filepath.Join(dir, "DVK-Uninstall.exe"))
}

func uninstall(args []string) error {
	dir := installDir()
	// Удалитель не может стереть сам себя: копия во временной папке
	// перезапускается и уже оттуда убирает папку установки.
	self, _ := os.Executable()
	if !slices.Contains(args, "/fromtemp") {
		if !confirm("Удаление DVK", "Удалить DVK из "+dir+"?\n\nНастройки, доступы и память окна (%APPDATA%\\kb-desktop) и ключи в Диспетчере учетных данных останутся.") {
			return errCancelled
		}
		tmp := filepath.Join(os.TempDir(), "DVK-Uninstall.exe")
		if err := copyFile(self, tmp); err != nil {
			return err
		}
		return startArgs(tmp, "/uninstall", "/fromtemp")
	}
	stopRunning(filepath.Join(dir, "kb.exe"))
	unregister()
	_ = os.Remove(startMenuLink())
	if err := os.RemoveAll(dir); err != nil && !strings.Contains(err.Error(), "cannot find") {
		return fmt.Errorf("папка %s удалена не вся: %w", dir, err)
	}
	message("DVK удален", "DVK удален.", mbInfo)
	return nil
}

func startMenuLink() string {
	return filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", appName+".lnk")
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o755)
}
