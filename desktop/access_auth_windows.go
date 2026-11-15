package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// «Доступы» на Windows (#622, этап 5; решение владельца 15.11): пароль
// показывается после Windows Hello, а без Hello — после пароля учетной записи
// Windows в системном окне «Безопасность Windows». Нет ни Hello, ни пароля —
// проверить человека нечем, пароль не показывается совсем. Буфер — с пометкой
// «не в журнал буфера и не в облако», стирается через 30 с, как на Маке.

func authNextTo(string) authHelper { return winAuth{} }

type winAuth struct{}

// errNoWindowsSignIn — у учетной записи нет ни Hello, ни пароля.
var errNoWindowsSignIn = errors.New("у учетной записи Windows нет ни пароля, ни Windows Hello — пароли в «Доступах» не показываются: задайте их в Параметры → Учетные записи → Варианты входа")

// helloScript спрашивает Windows Hello через WinRT UserConsentVerifier. Причина
// приходит переменной KB_REASON, а не текстом скрипта: кавычки в ней не
// становятся кодом. Выход 0 — подтверждено, 1 — отказ, 3 — Hello не настроен.
const helloScript = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Runtime.WindowsRuntime
$asTask = ([System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object { $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1' })[0]
function Await($op, $t) { $task = $asTask.MakeGenericMethod($t).Invoke($null, @($op)); $task.Wait(-1) | Out-Null; $task.Result }
$v = [Windows.Security.Credentials.UI.UserConsentVerifier, Windows.Security.Credentials.UI, ContentType = WindowsRuntime]
$a = Await ($v::CheckAvailabilityAsync()) ([Windows.Security.Credentials.UI.UserConsentVerifierAvailability])
if ($a -ne 'Available') { [Console]::Error.WriteLine("hello: $a"); exit 3 }
$r = Await ($v::RequestVerificationAsync($env:KB_REASON)) ([Windows.Security.Credentials.UI.UserConsentVerificationResult])
if ($r -eq 'Verified') { exit 0 }
[Console]::Error.WriteLine("hello: $r"); exit 1
`

// powershell — системный, по полному пути, а не первый в PATH.
func powershell() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
}

func (winAuth) Check(reason string) error {
	cmd := exec.Command(powershell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", helloScript)
	cmd.Env = append(os.Environ(), "KB_REASON=DVK: "+reason)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit, exited := errors.AsType[*exec.ExitError](err)
	switch {
	case err == nil:
		return nil
	case exited && exit.ExitCode() == 1:
		return fmt.Errorf("%w: %s", errAuthDenied, strings.TrimSpace(stderr.String()))
	case exited && exit.ExitCode() == 3:
		return passwordCheck(reason) // Hello не настроен — пароль учетной записи
	default:
		return fmt.Errorf("проверка Windows Hello: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
}

var (
	credui                  = syscall.NewLazyDLL("credui.dll")
	credUIPrompt            = credui.NewProc("CredUIPromptForWindowsCredentialsW")
	credUnPack              = credui.NewProc("CredUnPackAuthenticationBufferW")
	credPack                = credui.NewProc("CredPackAuthenticationBufferW")
	advapi32                = syscall.NewLazyDLL("advapi32.dll")
	logonUser               = advapi32.NewProc("LogonUserW")
	ole32                   = syscall.NewLazyDLL("ole32.dll")
	coTaskMemFree           = ole32.NewProc("CoTaskMemFree")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	globalAlloc             = kernel32.NewProc("GlobalAlloc")
	globalLock              = kernel32.NewProc("GlobalLock")
	globalUnlock            = kernel32.NewProc("GlobalUnlock")
	globalFree              = kernel32.NewProc("GlobalFree")
	rtlMoveMemory           = kernel32.NewProc("RtlMoveMemory")
	user32                  = syscall.NewLazyDLL("user32.dll")
	openClipboard           = user32.NewProc("OpenClipboard")
	closeClipboard          = user32.NewProc("CloseClipboard")
	emptyClipboard          = user32.NewProc("EmptyClipboard")
	setClipboardData        = user32.NewProc("SetClipboardData")
	registerClipboardFormat = user32.NewProc("RegisterClipboardFormatW")
	clipboardSequence       = user32.NewProc("GetClipboardSequenceNumber")
)

// credUIInfo — CREDUI_INFOW.
type credUIInfo struct {
	size    uint32
	parent  uintptr
	message *uint16
	caption *uint16
	banner  uintptr
}

const (
	credUIWinGeneric       = 0x1   // CREDUIWIN_GENERIC: пароль открытым текстом после распаковки
	credUIWinCurrentUser   = 0x200 // CREDUIWIN_ENUMERATE_CURRENT_USER: только текущий пользователь
	errorCancelled         = 1223
	logon32LogonNetwork    = 3
	logon32ProviderDefault = 0
)

// passwordCheck asks the Windows account password in the system dialog and
// lets Windows check it (LogonUser). Пароль живет в памяти только до проверки.
//
// ponytail: LogonUser проверяет любую учетную запись этого компьютера, не
// только вошедшую; окно перечисляет одну текущую, другую надо набрать вручную.
// Потолок — человек с паролем другой учетки этого же компьютера. Путь —
// сверить SID токена с SID процесса.
func passwordCheck(reason string) error {
	msg, _ := syscall.UTF16PtrFromString("DVK: " + reason)
	capt, _ := syscall.UTF16PtrFromString("Подтвердите, что это Вы")
	info := credUIInfo{message: msg, caption: capt}
	info.size = uint32(unsafe.Sizeof(info))
	in, inSize := currentUserBuffer()
	var pkg, outSize uint32
	var out *byte // память выдает CredUI, пишет указатель сюда
	var save int32
	r, _, _ := credUIPrompt.Call(uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&pkg)),
		in, uintptr(inSize), uintptr(unsafe.Pointer(&out)), uintptr(unsafe.Pointer(&outSize)),
		uintptr(unsafe.Pointer(&save)), credUIWinGeneric|credUIWinCurrentUser)
	if in != 0 {
		globalFree.Call(in) //nolint:errcheck // свой буфер из currentUserBuffer
	}
	switch r {
	case 0:
	case errorCancelled:
		return fmt.Errorf("%w: окно пароля закрыто", errAuthDenied)
	default:
		return fmt.Errorf("окно пароля Windows: %w", syscall.Errno(r))
	}
	defer func() {
		zero(unsafe.Slice(out, outSize))
		coTaskMemFree.Call(uintptr(unsafe.Pointer(out))) //nolint:errcheck // ничего не возвращает
	}()
	user, domain, pass := make([]uint16, 514), make([]uint16, 338), make([]uint16, 514)
	nu, nd, np := uint32(len(user)), uint32(len(domain)), uint32(len(pass))
	defer zero16(pass)
	ok, _, err := credUnPack.Call(0, uintptr(unsafe.Pointer(out)), uintptr(outSize), uintptr(unsafe.Pointer(&user[0])), uintptr(unsafe.Pointer(&nu)),
		uintptr(unsafe.Pointer(&domain[0])), uintptr(unsafe.Pointer(&nd)), uintptr(unsafe.Pointer(&pass[0])), uintptr(unsafe.Pointer(&np)))
	if ok == 0 {
		return fmt.Errorf("окно пароля Windows: %w", err)
	}
	if pass[0] == 0 {
		return errNoWindowsSignIn // пустой пароль: Windows его для проверки не принимает
	}
	u, d := splitAccount(syscall.UTF16ToString(user), syscall.UTF16ToString(domain))
	pu, _ := syscall.UTF16PtrFromString(u)
	var pd *uint16
	if d != "" {
		pd, _ = syscall.UTF16PtrFromString(d)
	}
	var token syscall.Handle
	ok, _, err = logonUser.Call(uintptr(unsafe.Pointer(pu)), uintptr(unsafe.Pointer(pd)), uintptr(unsafe.Pointer(&pass[0])),
		logon32LogonNetwork, logon32ProviderDefault, uintptr(unsafe.Pointer(&token)))
	if ok == 0 {
		return fmt.Errorf("%w: Windows не принял пароль (%w)", errAuthDenied, err)
	}
	_ = syscall.CloseHandle(token)
	return nil
}

// splitAccount: CredUI отдает «ДОМЕН\имя» одной строкой, LogonUser ждет их
// порознь; «имя@домен» (учетная запись Microsoft) идет как есть, без домена.
func splitAccount(user, domain string) (string, string) {
	if d, u, ok := strings.Cut(user, `\`); ok && domain == "" {
		return u, d
	}
	return user, domain
}

// currentUserBuffer заполняет окно именем вошедшего: человек вводит только пароль.
func currentUserBuffer() (uintptr, uint32) {
	name := os.Getenv("USERDOMAIN") + `\` + os.Getenv("USERNAME")
	pn, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, 0
	}
	empty, _ := syscall.UTF16PtrFromString("")
	var size uint32
	credPack.Call(0, uintptr(unsafe.Pointer(pn)), uintptr(unsafe.Pointer(empty)), 0, uintptr(unsafe.Pointer(&size))) //nolint:errcheck // первый вызов узнает размер
	if size == 0 {
		return 0, 0
	}
	buf, _, _ := globalAlloc.Call(0x40, uintptr(size)) // GPTR; освобождает passwordCheck через GlobalFree
	if buf == 0 {
		return 0, 0
	}
	if ok, _, _ := credPack.Call(0, uintptr(unsafe.Pointer(pn)), uintptr(unsafe.Pointer(empty)), buf, uintptr(unsafe.Pointer(&size))); ok == 0 {
		globalFree.Call(buf) //nolint:errcheck
		return 0, 0
	}
	return buf, size
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func zero16(b []uint16) {
	for i := range b {
		b[i] = 0
	}
}

// --- Буфер обмена -------------------------------------------------------------

// clipHold — сколько пароль живет в буфере, как kb-auth copy --hold 30 на Маке.
const clipHold = 30 * time.Second

var clipMu sync.Mutex

func (winAuth) Copy(secret string) error {
	clipMu.Lock()
	defer clipMu.Unlock()
	if err := openClip(); err != nil {
		return err
	}
	defer closeClipboard.Call() //nolint:errcheck
	emptyClipboard.Call()       //nolint:errcheck
	text, err := syscall.UTF16FromString(secret)
	if err != nil {
		return errors.New("в пароле нулевой символ — буфер его не примет")
	}
	if err := putClip(13, unsafe.Slice((*byte)(unsafe.Pointer(&text[0])), len(text)*2)); err != nil { // CF_UNICODETEXT
		zero16(text)
		return err
	}
	zero16(text)
	// Пометки Windows 10+: не в журнал буфера (Win+V), не в облачный буфер, не
	// мониторам буфера. Не встали — пароль все равно сотрется через 30 с.
	for _, f := range []string{"ExcludeClipboardContentFromMonitorProcessing", "CanIncludeInClipboardHistory", "CanUploadToCloudClipboard"} {
		pf, _ := syscall.UTF16PtrFromString(f)
		if id, _, _ := registerClipboardFormat.Call(uintptr(unsafe.Pointer(pf))); id != 0 {
			_ = putClip(id, []byte{0, 0, 0, 0}) // DWORD 0
		}
	}
	seq, _, _ := clipboardSequence.Call()
	time.AfterFunc(clipHold, func() { clearClipIf(seq) })
	return nil
}

// clearClipIf стирает буфер, только если в нем все еще наш пароль: скопированное
// человеком после этого не пропадет.
func clearClipIf(seq uintptr) {
	clipMu.Lock()
	defer clipMu.Unlock()
	if now, _, _ := clipboardSequence.Call(); now != seq {
		return
	}
	if openClip() != nil {
		return
	}
	emptyClipboard.Call() //nolint:errcheck
	closeClipboard.Call() //nolint:errcheck
}

// openClip: буфер бывает занят другой программой — несколько попыток.
func openClip() error {
	for range 10 {
		if r, _, _ := openClipboard.Call(0); r != 0 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return errors.New("буфер обмена занят другой программой — попробуйте еще раз")
}

func putClip(format uintptr, data []byte) error {
	h, _, _ := globalAlloc.Call(0x2, uintptr(len(data))) // GMEM_MOVEABLE
	if h == 0 {
		return errors.New("буфер обмена: нет памяти")
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		globalFree.Call(h) //nolint:errcheck
		return errors.New("буфер обмена: память не открылась")
	}
	rtlMoveMemory.Call(p, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data))) //nolint:errcheck // ничего не возвращает
	globalUnlock.Call(h)                                                         //nolint:errcheck
	if r, _, _ := setClipboardData.Call(format, h); r == 0 {
		globalFree.Call(h) //nolint:errcheck
		return errors.New("буфер обмена не принял пароль")
	}
	return nil // память теперь принадлежит буферу
}
