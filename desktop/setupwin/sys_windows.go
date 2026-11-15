package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

const (
	mbOK           = 0x0
	mbOKCancel     = 0x1
	mbIconInfo     = 0x40
	mbIconError    = 0x10
	mbIconQuestion = 0x20
	idOK           = 1
	mbInfo         = mbOK | mbIconInfo
	mbError        = mbOK | mbIconError
)

var messageBox = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

func message(title, text string, flags uintptr) uintptr {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	r, _, _ := messageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), flags)
	return r
}

func confirm(title, text string) bool { return message(title, text, mbOKCancel|mbIconQuestion) == idOK }

func powershell() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return cmd
}

// shortcut makes a .lnk через WScript.Shell; пути — переменными, не текстом скрипта.
func shortcut(link, target, icon string) error {
	cmd := hidden(exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		`$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:KB_LINK); $s.TargetPath = $env:KB_TARGET; $s.IconLocation = $env:KB_ICON; $s.WorkingDirectory = Split-Path $env:KB_TARGET; $s.Save()`))
	cmd.Env = append(os.Environ(), "KB_LINK="+link, "KB_TARGET="+target, "KB_ICON="+icon)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func register(dir, exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close() //nolint:errcheck
	for name, v := range map[string]string{
		"DisplayName": appName, "DisplayVersion": version, "Publisher": publisher,
		"DisplayIcon": filepath.Join(dir, "dvk.ico"), "InstallLocation": dir,
		"UninstallString": `"` + filepath.Join(dir, "DVK-Uninstall.exe") + `" /uninstall`,
	} {
		if err := k.SetStringValue(name, v); err != nil {
			return err
		}
	}
	for _, name := range []string{"NoModify", "NoRepair"} {
		if err := k.SetDWordValue(name, 1); err != nil {
			return err
		}
	}
	run, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer run.Close() //nolint:errcheck
	return run.SetStringValue(appName, `"`+exe+`"`)
}

func unregister() {
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstKey)
	if run, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		_ = run.DeleteValue(appName)
		_ = run.Close()
	}
}

// webView2Installed: Evergreen Runtime пишет версию в ключ EdgeUpdate —
// машинный (обе ветки реестра) или пользовательский.
func webView2Installed() bool {
	const id = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, p := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + id},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + id},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\` + id},
	} {
		k, err := registry.OpenKey(p.root, p.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		_ = k.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return true
		}
	}
	return false
}

func start(exe string) error { return startArgs(exe) }

func startArgs(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	return cmd.Start()
}

// stopRunning closes the installed DVK before its files are removed.
func stopRunning(exe string) {
	_ = hidden(exec.Command("taskkill", "/F", "/IM", filepath.Base(exe))).Run()
	time.Sleep(500 * time.Millisecond)
}
