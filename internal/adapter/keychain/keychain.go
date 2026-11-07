// Package keychain keeps secrets (the token of a kb server source) in the
// macOS Keychain through the system `security` tool — без cgo и зависимостей.
//
// Токен в config.json лежал бы рядом с путями к личным папкам в файле, который
// копируют в бэкапы; Связка ключей шифрует его и отдает только этому пользователю.
package keychain

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

var (
	// ErrNotFound — в связке нет секрета для этой учетки.
	ErrNotFound = errors.New("в Связке ключей нет такой записи")
	// ErrBadValue — пустой секрет или учетка со знаком, который сломал бы разбор
	// команды security -i (кавычка, обратная косая, пробел, перевод строки).
	ErrBadValue = errors.New("секрет пустой или учетка с кавычкой, обратной косой или пробелом")
	// ErrTooLong — строка команды security -i вышла бы за ее предел.
	ErrTooLong = errors.New("пароль длиннее 1900 байт — столько Связка через security не принимает")
)

// ponytail: пароль идет строкой security -i, а она выполняется до 4096 знаков
// (замер 07.11 на проверочной связке: 4096 — да, 4097 — молча нет); hex
// удваивает пароль, отсюда обещанный предел maxSecret с запасом на имя службы,
// учетки и путь. Чтение разбирает строку "password:" вывода -g — формат не
// описан в man, его держат тесты на настоящем security. Путь выше потолка —
// Security.framework в помощнике окна после замера ACL Связки (#494).
const (
	maxLine   = 4096
	maxSecret = 1900
)

// securityPath — системный security, а не первый в PATH: через него идут пароли.
const securityPath = "/usr/bin/security"

// Locked — Связка не ответила (заперта, отказ в доступе): одни слова в окне,
// поиске и MCP, в форме «что не так — что сделать».
const Locked = "Связка ключей не ответила — разблокируйте ее"

// notFoundExit — код выхода security, когда записи нет.
const notFoundExit = 44

// timeout — сколько ждать security: запертая связка ждет диалог разблокировки,
// и без предела вызов повесил бы экран источников и сохранение.
const timeout = 10 * time.Second

func command(args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	return exec.CommandContext(ctx, securityPath, args...), cancel
}

// Store is one service in a keychain: Service — имя приложения в Связке
// ключей, Keychain — путь к связке, пусто — login-связка пользователя.
type Store struct {
	Service  string
	Keychain string
}

// Set stores secret for account, replacing an existing one. Секрет уходит в
// security через stdin (режим -i), а не аргументом: аргумент видно в ps. И
// hex-строкой (-X): в строке команды только [0-9a-f], поэтому пробел, кавычка,
// обратная косая и не-ASCII в пароле разбор не ломают (#494).
func (s Store) Set(account, secret string) error {
	if !safe(account) || secret == "" {
		return ErrBadValue
	}
	line := fmt.Sprintf("add-generic-password -U -s %q -a %q -X %s", s.Service, account, hex.EncodeToString([]byte(secret)))
	if s.Keychain != "" {
		line += fmt.Sprintf(" %q", s.Keychain)
	}
	if len(secret) > maxSecret || len(line) > maxLine {
		return ErrTooLong
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

// Get returns the secret for account, or ErrNotFound. Читает -g, а не -w: -w
// печатает непечатный пароль hex-ом без пометки, и «deadbeef» не отличить от
// hex другого пароля; -g помечает hex префиксом 0x.
func (s Store) Get(account string) (string, error) {
	if !safe(account) {
		return "", ErrBadValue
	}
	cmd, cancel := command(s.args("find-generic-password", account, "-g")...)
	defer cancel()
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok && ee.ExitCode() == notFoundExit {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("security find-generic-password: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	for line := range strings.Lines(errOut.String()) {
		if v, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "password: "); ok {
			return parsePassword(v)
		}
	}
	return "", errors.New("security find-generic-password: в ответе нет пароля")
}

// parsePassword reads the value of security's "password: " line: печатный
// пароль — в кавычках как есть, остальной — 0xHEX и после пробела кавычки с
// восьмеричными escape; точные байты дает hex.
func parsePassword(v string) (string, error) {
	if h, ok := strings.CutPrefix(v, "0x"); ok {
		h, _, _ = strings.Cut(h, " ")
		b, err := hex.DecodeString(h)
		if err != nil {
			return "", fmt.Errorf("security find-generic-password: hex пароля: %w", err)
		}
		return string(b), nil
	}
	if v == "" {
		return "", nil // пустой пароль: "password: " без кавычек
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1], nil
	}
	return "", errors.New("security find-generic-password: пароль в непонятном виде")
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

// safe — для учетки: печатный ASCII без кавычки и обратной косой — %q для
// security -i записал бы не-ASCII как \uXXXX, а security такие escape не
// раскрывает. Секрет идет hex-ом и этой проверки не требует.
func safe(v string) bool {
	if v == "" {
		return false
	}
	for i := range len(v) {
		if c := v[i]; c < 0x21 || c > 0x7e || c == '"' || c == '\\' {
			return false
		}
	}
	return true
}
