package main

import (
	"log"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// Трей и Ctrl+Alt+Space на Windows (#622, этап 5; решения владельца 15.11):
// как меню-бар и ⌥Space на Маке. Крестик и Esc прячут окно, окно продолжает
// отвечать телефону; выход — из меню значка. Без cgo: служебное окно без
// видимой рамки (HWND_MESSAGE) получает и сообщения значка, и горячую клавишу
// в своей петле на отдельном потоке ОС.

const (
	wmApp       = 0x8000
	wmTray      = wmApp + 1
	wmHotkey    = 0x0312
	wmCommand   = 0x0111
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205
	wmDestroy   = 0x0002

	nimAdd    = 0
	nimDelete = 2
	nifMsg    = 0x1
	nifIcon   = 0x2
	nifTip    = 0x4

	modAlt      = 0x1
	modControl  = 0x2
	modNoRepeat = 0x4000
	vkSpace     = 0x20
	hotkeyID    = 1

	menuShow = 1
	menuQuit = 2

	tpmReturnCmd   = 0x100
	tpmRightButton = 0x2
	mfString       = 0x0
	mfSeparator    = 0x800
)

var (
	shell32             = syscall.NewLazyDLL("shell32.dll")
	shellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	registerClassEx     = user32.NewProc("RegisterClassExW")
	createWindowEx      = user32.NewProc("CreateWindowExW")
	defWindowProc       = user32.NewProc("DefWindowProcW")
	getMessage          = user32.NewProc("GetMessageW")
	translateMessage    = user32.NewProc("TranslateMessage")
	dispatchMessage     = user32.NewProc("DispatchMessageW")
	registerHotKey      = user32.NewProc("RegisterHotKey")
	loadIcon            = user32.NewProc("LoadIconW")
	createPopupMenu     = user32.NewProc("CreatePopupMenu")
	appendMenu          = user32.NewProc("AppendMenuW")
	trackPopupMenu      = user32.NewProc("TrackPopupMenu")
	destroyMenu         = user32.NewProc("DestroyMenu")
	getCursorPos        = user32.NewProc("GetCursorPos")
	setForegroundWindow = user32.NewProc("SetForegroundWindow")
	getForegroundWindow = user32.NewProc("GetForegroundWindow")
	getWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	getModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	trayOf              tray
	trayHwnd            uintptr
)

type wndClassEx struct {
	size, style                   uint32
	wndProc                       uintptr
	clsExtra, wndExtra            int32
	instance, icon, cursor, brush uintptr
	menuName, className           *uint16
	iconSm                        uintptr
}

// notifyIconData — NOTIFYICONDATAW версии 1 (szTip на 64 знака): этот размер
// Shell_NotifyIcon принимает на любой Windows, а поля новее значку не нужны.
type notifyIconData struct {
	size             uint32
	hwnd             uintptr
	id, flags, cbMsg uint32
	icon             uintptr
	tip              [64]uint16
}

type msg struct {
	hwnd           uintptr
	message        uint32
	wParam, lParam uintptr
	time           uint32
	x, y           int32
	private        uint32
}

// installMenuBar starts the tray thread: значок, меню, Ctrl+Alt+Space.
func installMenuBar(t tray) {
	trayOf = t
	go trayLoop()
}

func trayLoop() {
	runtime.LockOSThread() // окно и его сообщения живут на одном потоке ОС
	inst, _, _ := getModuleHandle.Call(0)
	cls, _ := syscall.UTF16PtrFromString("DVKTray")
	wc := wndClassEx{wndProc: syscall.NewCallback(trayProc), instance: inst, className: cls}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, err := registerClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		log.Printf("tray: класс окна не зарегистрирован: %v", err)
		return
	}
	const hwndMessage = ^uintptr(2) // HWND_MESSAGE = -3
	hwnd, _, err := createWindowEx.Call(0, uintptr(unsafe.Pointer(cls)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, inst, 0)
	if hwnd == 0 {
		log.Printf("tray: служебное окно не создано: %v", err)
		return
	}
	trayHwnd = hwnd
	addTrayIcon(hwnd, inst)
	if r, _, err := registerHotKey.Call(hwnd, hotkeyID, modControl|modAlt|modNoRepeat, vkSpace); r == 0 {
		log.Printf("tray: Ctrl+Alt+Space не зарегистрирована (%v): ее заняла другая программа; окно открывается из трея", err)
	}
	var m msg
	for {
		r, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&m))) //nolint:errcheck
		dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))  //nolint:errcheck
	}
}

func addTrayIcon(hwnd, inst uintptr) {
	// Значок приложения из ресурсов exe — группа №3 (winres, как winc.AppIconID
	// Wails); нет — стандартный значок Windows.
	icon, _, _ := loadIcon.Call(inst, 3)
	if icon == 0 {
		icon, _, _ = loadIcon.Call(0, 32512) // IDI_APPLICATION
	}
	nid := notifyIconData{hwnd: hwnd, id: 1, flags: nifMsg | nifIcon | nifTip, cbMsg: wmTray, icon: icon}
	nid.size = uint32(unsafe.Sizeof(nid))
	if tip, err := syscall.UTF16FromString("DVK · Ctrl+Alt+Space"); err == nil {
		copy(nid.tip[:len(nid.tip)-1], tip)
	}
	if r, _, err := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		log.Printf("tray: значок не добавлен: %v", err)
	}
}

// removeMenuBar убирает значок при выходе: иначе в трее остается призрак до наведения.
func removeMenuBar() { removeTrayIcon() }

func removeTrayIcon() {
	nid := notifyIconData{hwnd: trayHwnd, id: 1}
	nid.size = uint32(unsafe.Sizeof(nid))
	shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid))) //nolint:errcheck
}

func trayProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmHotkey:
		if wParam == hotkeyID {
			go trayOf.summon(ourWindowInFront())
		}
		return 0
	case wmTray:
		switch lParam & 0xffff {
		case wmLButtonUp:
			go trayOf.summon(false)
		case wmRButtonUp:
			trayMenu(hwnd)
		}
		return 0
	case wmDestroy:
		removeTrayIcon()
		return 0
	}
	r, _, _ := defWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func trayMenu(hwnd uintptr) {
	menu, _, _ := createPopupMenu.Call()
	if menu == 0 {
		return
	}
	defer destroyMenu.Call(menu) //nolint:errcheck
	show, _ := syscall.UTF16PtrFromString("Открыть DVK\tCtrl+Alt+Space")
	quit, _ := syscall.UTF16PtrFromString("Выйти")
	appendMenu.Call(menu, mfString, menuShow, uintptr(unsafe.Pointer(show))) //nolint:errcheck
	appendMenu.Call(menu, mfSeparator, 0, 0)                                 //nolint:errcheck
	appendMenu.Call(menu, mfString, menuQuit, uintptr(unsafe.Pointer(quit))) //nolint:errcheck
	var pt struct{ x, y int32 }
	getCursorPos.Call(uintptr(unsafe.Pointer(&pt))) //nolint:errcheck
	// Без SetForegroundWindow меню не закрывается щелчком мимо (известное правило Windows).
	setForegroundWindow.Call(hwnd) //nolint:errcheck
	cmd, _, _ := trackPopupMenu.Call(menu, tpmReturnCmd|tpmRightButton, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	switch cmd {
	case menuShow:
		go trayOf.summon(false)
	case menuQuit:
		removeTrayIcon()
		go trayOf.quit()
	}
}

// ourWindowInFront: окно DVK сейчас на переднем плане — горячая клавиша прячет.
func ourWindowInFront() bool {
	fg, _, _ := getForegroundWindow.Call()
	if fg == 0 {
		return false
	}
	var pid uint32
	getWindowThreadPID.Call(fg, uintptr(unsafe.Pointer(&pid))) //nolint:errcheck
	return int(pid) == os.Getpid()
}
