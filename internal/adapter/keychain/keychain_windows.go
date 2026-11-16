package keychain

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

// Диспетчер учетных данных Windows (#622, этап 5): тот же Store, что Связка на
// Маке. Запись — общая (CRED_TYPE_GENERIC) в хранилище пользователя: Windows
// шифрует ее ключом входа и отдает только этому пользователю, как Связка.
// Имя записи — «служба/учетка», в «Учетных данных Windows» ее видно и можно
// удалить руками. Keychain (путь к связке на Маке) здесь — приставка имени:
// тестовое хранилище не смешивается с настоящим.

var (
	// ErrNotFound — в Диспетчере учетных данных нет секрета для этой учетки.
	ErrNotFound = errors.New("в Диспетчере учетных данных Windows нет такой записи")
	// ErrBadValue — пустой секрет или учетка с пробелом, кавычкой или обратной косой.
	ErrBadValue = errors.New("секрет пустой или учетка с кавычкой, обратной косой или пробелом")
	// ErrTooLong — секрет больше, чем принимает Диспетчер учетных данных.
	ErrTooLong = errors.New("пароль длиннее 2560 байт — столько Диспетчер учетных данных Windows не принимает")
)

// Locked — хранилище не ответило: одни слова в окне, поиске и MCP.
const Locked = "Диспетчер учетных данных Windows не ответил — войдите в Windows заново"

const (
	credTypeGeneric  = 1    // CRED_TYPE_GENERIC
	credPersistLocal = 2    // CRED_PERSIST_LOCAL_MACHINE: остается после выхода, на этом компьютере
	maxBlob          = 2560 // CRED_MAX_CREDENTIAL_BLOB_SIZE (5*512)
	errNotFound      = syscall.Errno(1168)
)

// credential — CREDENTIALW из wincred.h, поля по порядку.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32   = syscall.NewLazyDLL("advapi32.dll")
	credWrite  = advapi32.NewProc("CredWriteW")
	credRead   = advapi32.NewProc("CredReadW")
	credDelete = advapi32.NewProc("CredDeleteW")
	credFree   = advapi32.NewProc("CredFree")
)

func (s Store) target(account string) string {
	t := s.Service + "/" + account
	if s.Keychain != "" {
		t = s.Keychain + "/" + t
	}
	return t
}

// Set stores secret for account, replacing an existing one.
func (s Store) Set(account, secret string) error {
	if !safe(account) || secret == "" {
		return ErrBadValue
	}
	if len(secret) > maxBlob {
		return ErrTooLong
	}
	name, err := syscall.UTF16PtrFromString(s.target(account))
	if err != nil {
		return ErrBadValue
	}
	user, err := syscall.UTF16PtrFromString(account)
	if err != nil {
		return ErrBadValue
	}
	blob := []byte(secret)
	c := credential{
		Type:               credTypeGeneric,
		TargetName:         name,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            credPersistLocal,
		UserName:           user,
	}
	r, _, callErr := credWrite.Call(uintptr(unsafe.Pointer(&c)), 0)
	runtime.KeepAlive(blob)
	runtime.KeepAlive(name)
	runtime.KeepAlive(user)
	if r == 0 {
		return fmt.Errorf("CredWrite: %w", callErr)
	}
	return nil
}

// Get returns the secret for account, or ErrNotFound.
func (s Store) Get(account string) (string, error) {
	if !safe(account) {
		return "", ErrBadValue
	}
	name, err := syscall.UTF16PtrFromString(s.target(account))
	if err != nil {
		return "", ErrBadValue
	}
	var p *credential
	r, _, callErr := credRead.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0, uintptr(unsafe.Pointer(&p)))
	runtime.KeepAlive(name)
	if r == 0 {
		if errors.Is(callErr, errNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("CredRead: %w", callErr)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(p))) //nolint:errcheck // CredFree ничего не возвращает
	if p.CredentialBlobSize == 0 || p.CredentialBlob == nil {
		return "", nil
	}
	// Копия до CredFree: память записи принадлежит Windows.
	return string(unsafe.Slice(p.CredentialBlob, p.CredentialBlobSize)), nil
}

// Delete removes the secret for account. Отсутствующая запись — не ошибка.
func (s Store) Delete(account string) error {
	if !safe(account) {
		return ErrBadValue
	}
	name, err := syscall.UTF16PtrFromString(s.target(account))
	if err != nil {
		return ErrBadValue
	}
	r, _, callErr := credDelete.Call(uintptr(unsafe.Pointer(name)), credTypeGeneric, 0)
	runtime.KeepAlive(name)
	if r == 0 && !errors.Is(callErr, errNotFound) {
		return fmt.Errorf("CredDelete: %w", callErr)
	}
	return nil
}
