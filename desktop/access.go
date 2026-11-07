package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/adapter/credsjson"
	"github.com/daniil/kb-engine/internal/adapter/keychain"
	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/creds"
)

// Экран «Доступы» (#494): карточки видны окну без пароля; пароль показать и
// скопировать — только после Touch ID, каждый раз. Touch ID и буфер — помощник
// kb-auth рядом с окном (LocalAuthentication, NSPasteboard со скрытой пометкой).

// errAuthDenied — человек не подтвердил Touch ID (отмена, чужой палец).
var errAuthDenied = errors.New("не подтвержден Touch ID")

// authHelper asks Touch ID and copies a secret to the clipboard.
type authHelper interface {
	Check(reason string) error
	Copy(secret string) error
}

// accessDesk is the access screen; auth nil — окно не из сборки, помощника нет.
type accessDesk struct {
	svc  creds.Service
	auth authHelper
}

func newAccessDesk(home string, auth authHelper) *accessDesk {
	return &accessDesk{
		svc:  creds.Service{Cards: credsjson.Store{Path: credsjson.DefaultPath(home)}, Secrets: windowSecrets{keychain.Store{Service: "DVK credentials"}}},
		auth: auth,
	}
}

// windowSecrets adds Has to the Keychain: «нет записи» — false, а не ошибка.
type windowSecrets struct{ keychain.Store }

func (k windowSecrets) Has(account string) (bool, error) {
	_, err := k.Get(account)
	if errors.Is(err, keychain.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// accessIn is a card typed in the window.
type accessIn struct {
	Product  string `json:"product"`
	Env      string `json:"env"`
	URL      string `json:"url"`
	Host     string `json:"host"`
	Login    string `json:"login"`
	Role     string `json:"role"`
	Rights   string `json:"rights"`
	IssuedBy string `json:"issuedBy"`
	IssuedAt string `json:"issuedAt"` // ГГГГ-ММ-ДД или пусто
	Note     string `json:"note"`
}

// accessView is a card for the page — без пароля.
type accessView struct {
	ID       string `json:"id"`
	Product  string `json:"product"`
	Env      string `json:"env"`
	EnvName  string `json:"envName"`
	Risky    bool   `json:"risky"`
	URL      string `json:"url"`
	Host     string `json:"host"`
	Login    string `json:"login"`
	Role     string `json:"role"`
	Rights   string `json:"rights"`
	IssuedBy string `json:"issuedBy"`
	IssuedAt string `json:"issuedAt"`
	Note     string `json:"note"`
}

// envNames — среда словами человека.
var envNames = map[domain.Env]string{domain.EnvProd: "прод", domain.EnvStage: "стенд", domain.EnvTest: "тест", domain.EnvDev: "dev"}

// List returns the cards with every word of q.
func (d *accessDesk) List(q string) ([]accessView, error) {
	list, err := d.svc.Find(q)
	if err != nil {
		return nil, err
	}
	out := make([]accessView, 0, len(list))
	for _, c := range list {
		v := accessView{ID: c.ID(), Product: c.Product(), Env: string(c.Env()), EnvName: envNames[c.Env()], Risky: c.Env().Risky(),
			URL: c.URL(), Host: c.Host(), Login: c.Login(), Role: c.Role(), Rights: c.Rights(), IssuedBy: c.IssuedBy(), Note: c.Note()}
		if !c.IssuedAt().IsZero() {
			v.IssuedAt = c.IssuedAt().Format(time.DateOnly)
		}
		out = append(out, v)
	}
	return out, nil
}

// Add stores a card typed in the window and its password.
func (d *accessDesk) Add(in accessIn, password string) error {
	dto := domain.CredentialDTO{Product: in.Product, Env: domain.Env(in.Env), URL: in.URL, Host: in.Host, Login: in.Login,
		Role: in.Role, Rights: in.Rights, IssuedBy: in.IssuedBy, Note: in.Note}
	if in.IssuedAt != "" {
		at, err := time.Parse(time.DateOnly, in.IssuedAt)
		if err != nil {
			return fmt.Errorf("дата выдачи %q — нужна ГГГГ-ММ-ДД", in.IssuedAt)
		}
		dto.IssuedAt = at
	}
	c, err := domain.NewCredential(dto)
	if err != nil {
		return err
	}
	pw, err := domain.NewPassword(password)
	if err != nil {
		return err
	}
	return d.svc.Add(c, pw)
}

func (d *accessDesk) check(reason string) error {
	if d.auth == nil {
		return errors.New("нет помощника kb-auth: Touch ID работает в собранном окне")
	}
	return d.auth.Check(reason)
}

// Open asks Touch ID to open the container.
func (d *accessDesk) Open() error {
	return d.check("открыть хранилище доступов")
}

// Reveal returns the password after Touch ID: показать в окне на 30 с.
func (d *accessDesk) Reveal(id string) (string, error) {
	if err := d.check("показать пароль"); err != nil {
		return "", err
	}
	return d.svc.Password(id)
}

// Copy puts the password on the clipboard after Touch ID.
func (d *accessDesk) Copy(id string) error {
	if err := d.check("скопировать пароль"); err != nil {
		return err
	}
	pw, err := d.svc.Password(id)
	if err != nil {
		return err
	}
	return d.auth.Copy(pw)
}

// kbAuth runs the kb-auth helper next to the window's binary.
type kbAuth struct{ bin string }

// authNextTo finds kb-auth next to the window; нет — nil (окно не из сборки).
func authNextTo(exe string) authHelper {
	bin := filepath.Join(filepath.Dir(exe), "kb-auth")
	if info, err := os.Stat(bin); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
		return kbAuth{bin: bin}
	}
	return nil
}

func (a kbAuth) Check(reason string) error {
	var stderr strings.Builder
	cmd := exec.Command(a.bin, "check", reason)
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return fmt.Errorf("%w: %s", errAuthDenied, strings.TrimSpace(stderr.String()))
	default:
		return fmt.Errorf("проверка Touch ID: %s: %w", strings.TrimSpace(stderr.String()), err)
	}
}

// Copy hands the secret to kb-auth through stdin (не аргументом: его видно в
// ps), ждет "copied" и отпускает процесс: тот сам сотрет буфер через 30 с.
func (a kbAuth) Copy(secret string) error {
	cmd := exec.Command(a.bin, "copy", "--hold", "30")
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, werr := io.WriteString(in, secret)
	_ = in.Close()
	line, rerr := bufio.NewReader(out).ReadString('\n')
	go func() { _ = cmd.Wait() }() // помощник живет свои 30 с и выходит сам
	if werr != nil {
		return werr
	}
	if strings.TrimSpace(line) != "copied" {
		return fmt.Errorf("буфер не принял пароль: %w", rerr)
	}
	return nil
}
