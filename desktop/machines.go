package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/machines"
)

// Машины в окне (#549): те же ворота, что у доступов. Список, запись и удаление
// только в открытом хранилище; пароль (если вход по паролю) лежит в Связке ключей
// под именем machine:<id> и выходит только после Touch ID. Двоеточие в имени не
// даст столкнуться с id карточек доступов (в них его нет).

// machineIn is a machine typed in the window.
type machineIn struct {
	Name      string                  `json:"name"`
	Group     string                  `json:"group"`
	Env       string                  `json:"env"`
	Host      string                  `json:"host"`
	Port      int                     `json:"port"`
	User      string                  `json:"user"`
	Auth      string                  `json:"auth"`
	KeyPath   string                  `json:"keyPath"`
	Jump      string                  `json:"jump"`
	TokenHint string                  `json:"tokenHint"`
	Note      string                  `json:"note"`
	Projects  []domain.MachineProject `json:"projects"`
}

func (in machineIn) dto() domain.MachineDTO {
	return domain.MachineDTO{Name: in.Name, Group: in.Group, Env: domain.Env(in.Env), Host: in.Host, Port: in.Port, User: in.User,
		Auth: domain.Auth(in.Auth), KeyPath: in.KeyPath, Jump: in.Jump, TokenHint: in.TokenHint, Note: in.Note, Projects: in.Projects}
}

// machineView is a machine for the page — без пароля. Окно показывает человеку
// и путь ключа, и заметку (в отличие от агента), но только в открытом хранилище.
type machineView struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Group       string                  `json:"group"`
	Env         string                  `json:"env"`
	EnvName     string                  `json:"envName"`
	Risky       bool                    `json:"risky"`
	Host        string                  `json:"host"`
	Port        int                     `json:"port"`
	User        string                  `json:"user"`
	Auth        string                  `json:"auth"`
	KeyPath     string                  `json:"keyPath"`
	Jump        string                  `json:"jump"`
	TokenHint   string                  `json:"tokenHint"`
	Note        string                  `json:"note"`
	Projects    []domain.MachineProject `json:"projects"`
	Command     string                  `json:"command"`    // пусто, если цепочка не собирается
	ChainError  string                  `json:"chainError"` // какой бастион пропал
	HasPassword bool                    `json:"hasPassword"`
}

// nonNil turns a nil list into an empty one: JSON null окно не читает (m.projects.length).
func nonNil[T any](xs []T) []T {
	if xs == nil {
		return []T{}
	}
	return xs
}

// machineSecret is the Keychain account of a machine password.
func machineSecret(id string) string { return "machine:" + id }

// Machines returns the machines with every word of q — только в открытом хранилище.
func (d *accessDesk) Machines(q string) ([]machineView, error) {
	if !d.isOpen() {
		return nil, errVaultClosed
	}
	list, err := d.machines.Find(q)
	if err != nil {
		return nil, err
	}
	out := make([]machineView, 0, len(list))
	for _, m := range list {
		v := machineView{ID: m.ID(), Name: m.Name(), Group: m.Group(), Env: string(m.Env()), EnvName: envNames[m.Env()], Risky: m.Env().Risky(),
			Host: m.Host(), Port: m.Port(), User: m.User(), Auth: string(m.Auth()), KeyPath: m.KeyPath(), Jump: m.Jump(),
			TokenHint: m.TokenHint(), Note: m.Note(), Projects: nonNil(m.Projects())}
		switch cmd, err := d.machines.Command(m.ID()); {
		case errors.Is(err, domain.ErrMachineChain):
			v.ChainError = err.Error()
		case err != nil:
			return nil, err
		default:
			v.Command = cmd
		}
		if m.Auth() == domain.AuthPassword {
			if v.HasPassword, err = d.svc.Secrets.Has(machineSecret(m.ID())); err != nil {
				return nil, fmt.Errorf("машины: Связка ключей не ответила: %w", err)
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// AddMachine stores a machine typed in the window and, for a password sign-in, its
// password. Пароль нужен только при входе по паролю. Машина с тем же id или пароль
// с этим id уже в Связке — отказ, чужое не тронуто; не записалась карточка —
// снимается только пароль, положенный этим вызовом.
func (d *accessDesk) AddMachine(in machineIn, password string) error {
	if !d.isOpen() {
		return errVaultClosed
	}
	m, err := domain.NewMachine(in.dto())
	if err != nil {
		return err
	}
	acct := machineSecret(m.ID())
	if m.Auth() != domain.AuthPassword {
		if strings.TrimSpace(password) != "" {
			return fmt.Errorf("%w: пароль нужен только при входе по паролю", domain.ErrInvalidMachine)
		}
		return d.machines.Add(m)
	}
	pw, err := domain.NewPassword(password)
	if err != nil {
		return fmt.Errorf("%w: при входе по паролю нужен пароль", domain.ErrInvalidMachine)
	}
	switch has, err := d.svc.Secrets.Has(acct); {
	case err != nil:
		return fmt.Errorf("машины: Связка ключей не ответила: %w", err)
	case has:
		return fmt.Errorf("%w: пароль %s уже лежит в Связке ключей", domain.ErrMachineExists, m.ID())
	}
	if err := d.svc.Secrets.Set(acct, pw.Reveal()); err != nil {
		return fmt.Errorf("машины: пароль не лег в Связку ключей: %w", err)
	}
	if err := d.machines.Add(m); err != nil {
		if derr := d.svc.Secrets.Delete(acct); derr != nil {
			return errors.Join(err, fmt.Errorf("машины: пароль %s остался в Связке ключей: %w", m.ID(), derr))
		}
		return err
	}
	return nil
}

// UpdateMachine changes a machine; пароль пустой — прежний остается, новый заменяет.
// Не записалась карточка — пароль возвращается прежним.
func (d *accessDesk) UpdateMachine(id string, in machineIn, password string) error {
	if !d.isOpen() {
		return errVaultClosed
	}
	dto := in.dto()
	dto.ID = id
	m, err := domain.NewMachine(dto)
	if err != nil {
		return err
	}
	acct := machineSecret(id)
	wantPW := m.Auth() == domain.AuthPassword
	undo, hadOld, err := d.stagePassword(acct, wantPW, password)
	if err != nil {
		return err
	}
	if err := d.machines.Update(id, dto); err != nil {
		undo()
		return err
	}
	if !wantPW && hadOld { // перешли с пароля на ключ: старый пароль больше не нужен
		return d.svc.Secrets.Delete(acct)
	}
	return nil
}

// stagePassword puts a new password into the Keychain when the window sent one and
// returns what undoes it; при входе по паролю без пароля в Связке и без нового —
// отказ, а при другом способе входа пароль присылать нельзя.
func (d *accessDesk) stagePassword(acct string, wantPW bool, password string) (undo func(), hadOld bool, err error) {
	old, getErr := d.svc.Secrets.Get(acct)
	hadOld, given := getErr == nil, strings.TrimSpace(password) != ""
	switch {
	case !wantPW && given:
		return nil, hadOld, fmt.Errorf("%w: пароль нужен только при входе по паролю", domain.ErrInvalidMachine)
	case wantPW && !given && !hadOld:
		return nil, hadOld, fmt.Errorf("%w: при входе по паролю нужен пароль", domain.ErrInvalidMachine)
	case wantPW && given:
		if err := d.svc.Secrets.Set(acct, password); err != nil {
			return nil, hadOld, fmt.Errorf("машины: пароль не лег в Связку ключей: %w", err)
		}
		return func() {
			if hadOld {
				_ = d.svc.Secrets.Set(acct, old)
			} else {
				_ = d.svc.Secrets.Delete(acct)
			}
		}, hadOld, nil
	}
	return func() {}, hadOld, nil
}

// DeleteMachine removes a machine after Touch ID — только в открытом хранилище:
// удаление не отменить. Пароль снимается вместе с машиной.
func (d *accessDesk) DeleteMachine(id string) error {
	if !d.isOpen() {
		return errVaultClosed
	}
	if err := d.check("снять машину"); err != nil {
		return err
	}
	if !d.isOpen() { // закрылось по таймеру, пока висел диалог Touch ID
		return errVaultClosed
	}
	if err := d.machines.Delete(id); err != nil {
		return err
	}
	return d.svc.Secrets.Delete(machineSecret(id))
}

// RevealMachinePassword returns the password after Touch ID: показать в окне на 30 с.
func (d *accessDesk) RevealMachinePassword(id string) (string, error) {
	if err := d.check("показать пароль машины"); err != nil {
		return "", err
	}
	return d.svc.Secrets.Get(machineSecret(id))
}

// CopyMachinePassword puts the password on the clipboard after Touch ID.
func (d *accessDesk) CopyMachinePassword(id string) error {
	if err := d.check("скопировать пароль машины"); err != nil {
		return err
	}
	pw, err := d.svc.Secrets.Get(machineSecret(id))
	if err != nil {
		return err
	}
	return d.auth.Copy(pw)
}

// MachineScanPlan is what a scan does on the machine, step by step: человек
// читает это до запуска.
func (d *accessDesk) MachineScanPlan() []string { return machines.ScanPlan() }

// MachineScan reads what lies on the machine over ssh — только чтение, только в
// открытом хранилище; вход по ключу или агенту ssh на этом Mac.
func (d *accessDesk) MachineScan(id string) ([]machines.Found, error) {
	if !d.isOpen() {
		return nil, errVaultClosed
	}
	var run machines.Runner = sshRunner{}
	if d.scanner != nil {
		run = d.scanner
	}
	return d.machines.Scan(context.Background(), id, run)
}

// Скан не ждет вечно: соединение 10 секунд, весь запуск 30, вывод до 1 МиБ.
const (
	scanTimeout = 30 * time.Second
	maxScanOut  = 1 << 20
)

// sshRunner runs the system ssh without questions: BatchMode не просит пароль и
// не принимает ключ хоста молча (неизвестный хост — отказ с понятным текстом).
type sshRunner struct{ bin string } // bin пусто — ssh из PATH

// Run runs remote on the machine reached by args; ошибка — сообщение ssh, без
// вывода машины: в нем может быть что угодно.
func (r sshRunner) Run(ctx context.Context, args []string, remote string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	full := append([]string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}, args...)
	cmd := exec.CommandContext(ctx, cmp.Or(r.bin, "ssh"), append(full, remote)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("ssh не запустился: %w", err)
	}
	out, _ := io.ReadAll(io.LimitReader(stdout, maxScanOut))
	if len(out) == maxScanOut {
		cancel() // вывод больше предела: машина не должна завесить окно потоком
	}
	if err := cmd.Wait(); err != nil && len(out) < maxScanOut {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		return "", fmt.Errorf("ssh: %s", cmp.Or(msg, err.Error()))
	}
	return string(out), nil
}
