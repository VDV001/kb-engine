// Package keychain keeps secrets (the token of a kb server source) in the
// macOS Keychain through the system `security` tool — без cgo и зависимостей.
//
// Токен в config.json лежал бы рядом с путями к личным папкам в файле, который
// копируют в бэкапы; Связка ключей шифрует его и отдает только этому пользователю.
package keychain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var (
	// ErrNotFound — в связке нет секрета для этой учетки.
	ErrNotFound = errors.New("в Связке ключей нет такой записи")
	// ErrBadValue — пустое значение или символ, который сломал бы разбор
	// команды security -i (кавычка, обратная косая, пробел, перевод строки).
	ErrBadValue = errors.New("значение пустое или с кавычкой, обратной косой или пробелом")
)

// notFoundExit — код выхода security, когда записи нет.
const notFoundExit = 44

// timeout — сколько ждать security: запертая связка ждет диалог разблокировки,
// и без предела вызов повесил бы экран источников и сохранение.
const timeout = 10 * time.Second

func command(args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return exec.CommandContext(ctx, "security", args...), cancel
}

// Store is one service in a keychain: Service — имя приложения в Связке
// ключей, Keychain — путь к связке, пусто — login-связка пользователя.
type Store struct {
	Service  string
	Keychain string
}

// Set stores secret for account, replacing an existing one. Секрет уходит в
// security через stdin (режим -i), а не аргументом: аргумент видно в ps.
func (s Store) Set(account, secret string) error {
	if !safe(account) || !safe(secret) {
		return ErrBadValue
	}
	line := fmt.Sprintf("add-generic-password -U -s %q -a %q -w %q", s.Service, account, secret)
	if s.Keychain != "" {
		line += fmt.Sprintf(" %q", s.Keychain)
	}
	cmd, cancel := command("-i")
	defer cancel()
	cmd.Stdin = strings.NewReader(line + "\n")
	var errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &errOut, &errOut
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("security add-generic-password: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	// security -i выходит с нулем, даже если команда внутри отказала: успех
	// проверяется чтением, а не кодом выхода.
	if got, err := s.Get(account); err != nil || got != secret {
		return fmt.Errorf("security add-generic-password: запись не сохранилась: %s", strings.TrimSpace(errOut.String()))
	}
	return nil
}

// Get returns the secret for account, or ErrNotFound.
func (s Store) Get(account string) (string, error) {
	if !safe(account) {
		return "", ErrBadValue
	}
	cmd, cancel := command(s.args("find-generic-password", account, "-w")...)
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == notFoundExit {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("security find-generic-password: %w", err)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// Delete removes the secret for account. Отсутствующая запись — не ошибка:
// убрать источник без токена можно.
func (s Store) Delete(account string) error {
	if !safe(account) {
		return ErrBadValue
	}
	cmd, cancel := command(s.args("delete-generic-password", account)...)
	defer cancel()
	err := cmd.Run()
	if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == notFoundExit {
		return nil
	}
	if err != nil {
		return fmt.Errorf("security delete-generic-password: %w", err)
	}
	return nil
}

func (s Store) args(cmd, account string, extra ...string) []string {
	a := append([]string{cmd, "-s", s.Service, "-a", account}, extra...)
	if s.Keychain != "" {
		a = append(a, s.Keychain)
	}
	return a
}

func safe(v string) bool {
	return v != "" && !strings.ContainsAny(v, "\"\\ \t\r\n")
}
