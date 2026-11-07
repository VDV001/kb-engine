package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/credsjson"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/creds"
)

// runCreds is `kbengine creds add|find|copy` — доступы к продуктам (#494).
// Карточка (продукт, среда, адрес, логин, роль) — в файле рядом с настройками
// окна, ее видно поиском. Пароль — только в Связке ключей: add читает его из
// stdin (в терминале без эха), copy кладет в буфер на 30 с. Печатать пароль
// команда не умеет вовсе: вывод терминала уходит в историю оболочки, журнал
// агента и переписку с моделью.
func runCreds(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(stdout, credsUsage)
		return 0
	}
	if len(args) > 0 && args[0] == credsClearVerb {
		return credsClear(args[1:], stdin)
	}
	sub := map[string]func() int{
		"add":  func() int { return credsAdd(args[1:], stdin, stdout, stderr) },
		"find": func() int { return credsFind(args[1:], stdout, stderr) },
		"copy": func() int { return credsCopy(args[1:], stdout, stderr) },
	}
	if len(args) == 0 || sub[args[0]] == nil {
		fmt.Fprintln(stderr, credsUsage)
		return 2
	}
	return sub[args[0]]()
}

const credsUsage = `usage: kbengine creds add --product P --env прод|стенд|тест|dev --login L (--url U | --host H) [--role R] [--rights X] [--by B] [--at ГГГГ-ММ-ДД] [--note N] [--id ID]   (пароль — из stdin)
       kbengine creds find [слова]
       kbengine creds copy <id>   (пароль в буфер на 30 с; в терминал не печатается никогда)
       общий флаг: --file <путь к карточкам>`

// credsSecretStore is the password store the commands use.
type credsSecretStore = creds.Secrets

// credsSecrets opens the Keychain; тесты подменяют, чтобы не трогать Связку владельца.
var credsSecrets = func() credsSecretStore { return keychainSecrets{keychain.Store{Service: "DVK credentials"}} }

// keychainSecrets adds Has to the Keychain: «нет записи» — false, а не ошибка.
type keychainSecrets struct{ keychain.Store }

func (k keychainSecrets) Has(account string) (bool, error) {
	_, err := k.Get(account)
	if errors.Is(err, keychain.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// credsClipboard puts the password on the clipboard and schedules its removal.
var credsClipboard = clipboardFor30s

// credsDefaultFile — рядом с настройками окна: окно и терминал видят одни карточки.
func credsDefaultFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Application Support", "kb-desktop", "credentials.json")
}

func credsService(file string) creds.Service {
	return creds.Service{Cards: credsjson.Store{Path: file}, Secrets: credsSecrets()}
}

func credsAdd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("creds add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", credsDefaultFile(), "файл карточек")
	var d domain.CredentialDTO
	var env, at string
	fs.StringVar(&d.ID, "id", "", "id (иначе из продукта, среды и логина)")
	fs.StringVar(&d.Product, "product", "", "продукт")
	fs.StringVar(&env, "env", "", "среда: прод, стенд, тест, dev")
	fs.StringVar(&d.Login, "login", "", "логин")
	fs.StringVar(&d.URL, "url", "", "адрес входа")
	fs.StringVar(&d.Host, "host", "", "хост")
	fs.StringVar(&d.Role, "role", "", "роль")
	fs.StringVar(&d.Rights, "rights", "", "права")
	fs.StringVar(&d.IssuedBy, "by", "", "кто выдал")
	fs.StringVar(&at, "at", "", "когда выдан, ГГГГ-ММ-ДД")
	fs.StringVar(&d.Note, "note", "", "заметка")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	d.Env = domain.Env(env)
	if at != "" {
		t, err := time.Parse(time.DateOnly, at)
		if err != nil {
			fmt.Fprintf(stderr, "creds add: дата %q — нужна ГГГГ-ММ-ДД\n", at)
			return 2
		}
		d.IssuedAt = t
	}
	c, err := domain.NewCredential(d)
	if err != nil {
		fmt.Fprintln(stderr, "creds add:", err)
		return 1
	}
	raw, err := readPassword(stdin, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "creds add:", err)
		return 1
	}
	pw, err := domain.NewPassword(raw)
	if err != nil {
		fmt.Fprintln(stderr, "creds add:", err)
		return 1
	}
	if err := credsService(*file).Add(c, pw); err != nil {
		fmt.Fprintln(stderr, "creds add:", err)
		if errors.Is(err, domain.ErrCredentialExists) && strings.Contains(err.Error(), "Связке") {
			// Сирота: пароль лег, а карточка — нет (прерывание между ними).
			fmt.Fprintf(stderr, "creds add: если карточки %s нет нигде, это сирота прерванного add: security delete-generic-password -s \"DVK credentials\" -a %s\n", c.ID(), c.ID())
		}
		if errors.Is(err, keychain.ErrBadValue) {
			// ponytail: security -i не принимает в значении пробел, кавычку, \ и не-ASCII.
			// Потолок — такие пароли из терминала не сохранить. Путь — запись через
			// Security.framework (помощник окна на Swift, PR 3 #494) вместо security -i.
			fmt.Fprintln(stderr, "creds add: Связка через security не принимает пароль с пробелом, кавычкой, обратной косой или не латиницей — сохраните его из окна DVK")
		}
		return 1
	}
	fmt.Fprintf(stdout, "добавлен доступ %s: %s · %s · %s; пароль — в Связке ключей\n", c.ID(), c.Product(), envName[c.Env()], c.Login())
	return 0
}

// readPassword reads one line; в терминале — без эха (stty), чтобы пароль не
// остался на экране и в записи сессии.
func readPassword(stdin io.Reader, stderr io.Writer) (string, error) {
	if f, ok := stdin.(*os.File); ok {
		if st, err := f.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Fprint(stderr, "пароль (не печатается): ")
			if stty(f, "-echo") == nil {
				// Ctrl-C на вводе оставил бы терминал без эха: вернуть его и выйти.
				sig := make(chan os.Signal, 1)
				signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
				done := make(chan struct{})
				go func() {
					select {
					case s := <-sig:
						_ = stty(f, "echo")
						fmt.Fprintln(stderr)
						code := 130 // 128 + номер сигнала, как у оболочки
						if n, ok := s.(syscall.Signal); ok {
							code = 128 + int(n)
						}
						os.Exit(code)
					case <-done:
					}
				}()
				defer func() { signal.Stop(sig); close(done); _ = stty(f, "echo"); fmt.Fprintln(stderr) }()
			}
		}
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("не прочитать пароль: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func stty(tty *os.File, mode string) error {
	cmd := exec.Command("stty", mode)
	cmd.Stdin = tty
	return cmd.Run()
}

var envName = map[domain.Env]string{domain.EnvProd: "прод", domain.EnvStage: "стенд", domain.EnvTest: "тест", domain.EnvDev: "dev"}

func credsFind(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("creds find", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", credsDefaultFile(), "файл карточек")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	list, err := credsService(*file).Find(strings.Join(fs.Args(), " "))
	if err != nil {
		fmt.Fprintln(stderr, "creds find:", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "ничего не нашлось")
		return 0
	}
	for _, c := range list {
		where := c.URL()
		if where == "" {
			where = c.Host()
		}
		fmt.Fprintf(stdout, "%-28s %-16s %-6s %-16s %-16s %s\n", c.ID(), c.Product(), envName[c.Env()], c.Login(), c.Role(), where)
	}
	return 0
}

func credsCopy(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("creds copy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", credsDefaultFile(), "файл карточек")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, credsUsage)
		return 2
	}
	id := fs.Arg(0)
	pw, err := credsService(*file).Password(id)
	if err != nil {
		fmt.Fprintln(stderr, "creds copy:", err)
		return 1
	}
	if err := credsClipboard(pw); err != nil {
		fmt.Fprintln(stderr, "creds copy: буфер:", err)
		return 1
	}
	fmt.Fprintf(stdout, "пароль %s в буфере на 30 с; в терминал не печатается\n", id)
	return 0
}

// credsClearVerb — скрытая подкоманда: процесс, который через 30 с стирает
// буфер. Пароль ему не передается — только отпечаток, и через stdin, а не
// аргументом (аргументы видно в ps).
const credsClearVerb = "__clear-clipboard"

const clipboardHold = 30 * time.Second

func clipSum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// clipboardStillOurs reports whether the clipboard still holds our password:
// человек успел скопировать другое — его буфер не трогаем.
func clipboardStillOurs(current, sum string) bool { return current != "" && clipSum(current) == sum }

// clipboardFor30s copies the password with pbcopy and starts a detached
// cleaner that outlives this command.
//
// ponytail: pbcopy не помечает данные скрытыми (org.nspasteboard.ConcealedType):
// менеджеры буфера (Maccy, Raycast) запишут пароль в свою историю, Universal
// Clipboard отправит на другие устройства — «30 с» держит только сам буфер.
// Путь — копия через NSPasteboard с ConcealedType в помощнике окна (PR 3 #494).
func clipboardFor30s(secret string) error {
	cp := exec.Command("pbcopy")
	cp.Stdin = strings.NewReader(secret)
	if err := cp.Run(); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("пароль в буфере, но стереть его через 30 с некому: %w", err)
	}
	cl := exec.Command(self, "creds", credsClearVerb)
	cl.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // переживает закрытие терминала
	in, err := cl.StdinPipe()
	if err != nil {
		return err
	}
	if err := cl.Start(); err != nil {
		return fmt.Errorf("пароль в буфере, но стереть его через 30 с некому: %w", err)
	}
	_, werr := io.WriteString(in, clipSum(secret)+"\n")
	_ = in.Close()
	_ = cl.Process.Release()
	return werr
}

// credsClear waits and clears the clipboard if it still holds the password
// whose fingerprint came on stdin.
func credsClear(_ []string, stdin io.Reader) int {
	sum, _ := bufio.NewReader(stdin).ReadString('\n')
	sum = strings.TrimSpace(sum)
	time.Sleep(clipboardHold)
	cur, err := exec.Command("pbpaste").Output()
	if err != nil || !clipboardStillOurs(string(cur), sum) {
		return 0
	}
	clr := exec.Command("pbcopy")
	clr.Stdin = strings.NewReader("")
	_ = clr.Run()
	return 0
}
