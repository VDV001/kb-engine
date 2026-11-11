package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Машина (#549, решение владельца 11.11): сервер, на котором лежит много
// проектов. Это не учетная запись на стенде (Credential: продукт, среда, логин),
// поэтому свои инварианты: обязательны имя, хост и пользователь, среда нет, у
// машины есть порт, способ входа, цепочка через бастион и список проектов.
// Пароля в карточке нет по устройству, как у доступов: он в Связке ключей, а
// карточку видят поиск окна и агент через MCP; все, что уходит агенту, уходит
// провайдеру модели.

// ErrInvalidMachine — карточка машины нарушает инвариант.
var ErrInvalidMachine = errors.New("invalid machine")

// ErrMachineExists — машина с таким id уже есть.
var ErrMachineExists = errors.New("machine already exists")

// ErrMachineNotFound — машины с таким id нет.
var ErrMachineNotFound = errors.New("machine not found")

// ErrMachineChain — цепочка через бастион не собирается: нет машины или петля.
var ErrMachineChain = errors.New("machine chain")

// Auth — как входят на машину.
type Auth string

// Способы входа. Бастион не способ, а отдельное поле Jump: зайти через
// бастион можно и по паролю, и по ключу.
const (
	AuthPassword Auth = "password" // пароль в Связке ключей за Touch ID
	AuthKey      Auth = "key"      // ключ ssh: хранится только путь к файлу
	AuthToken    Auth = "token"    // сертификат или токен: в записи только пометка, где взять
)

var authWords = map[string]Auth{
	"password": AuthPassword, "пароль": AuthPassword,
	"key": AuthKey, "ключ": AuthKey, "ssh": AuthKey,
	"token": AuthToken, "токен": AuthToken, "сертификат": AuthToken, "cert": AuthToken,
}

// ParseAuth reads a sign-in method as people write it; незнакомое слово — отказ.
func ParseAuth(s string) (Auth, error) {
	if a, ok := authWords[strings.ToLower(strings.TrimSpace(s))]; ok {
		return a, nil
	}
	return "", fmt.Errorf("%w: способ входа %q — пароль, ключ или токен", ErrInvalidMachine, s)
}

// MachineProject — то, что лежит на машине: проект, путь, порт, пометка.
type MachineProject struct {
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
	Port string `json:"port,omitempty"`
	What string `json:"what,omitempty"`
}

// MachineDTO is a machine card as stored and typed: поля открыты, правила
// проверяет NewMachine. ID пустой — собирается из имени.
type MachineDTO struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Group     string           `json:"group,omitempty"`
	Env       Env              `json:"env,omitempty"`
	Host      string           `json:"host"`
	Port      int              `json:"port,omitempty"`
	User      string           `json:"user"`
	Auth      Auth             `json:"auth"`
	KeyPath   string           `json:"key_path,omitempty"`
	Jump      string           `json:"jump,omitempty"`
	TokenHint string           `json:"token_hint,omitempty"`
	Note      string           `json:"note,omitempty"`
	Projects  []MachineProject `json:"projects,omitempty"`
}

// Machine is a validated machine card without a password.
type Machine struct{ d MachineDTO }

// Команда ssh копируется человеком в терминал, поэтому хост и пользователь не
// могут стать опцией (-oProxyCommand=…) или двумя словами: первый знак не дефис,
// пробелов и кавычек нет. Хост допускает скобки и двоеточия для IPv6.
var (
	sshHost = regexp.MustCompile(`^[A-Za-z0-9_.:\[\]][A-Za-z0-9_.:\[\]-]*$`)
	sshUser = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
)

// defaultSSHPort — порт, если не указан.
const defaultSSHPort = 22

// NewMachine validates a card: имя, хост и пользователь обязательны; порт 1-65535
// (пустой — 22); способ входа из известных (пустой — ключ); среда необязательна,
// но если есть, то известная; id — латиница, цифры и дефис, до 64 знаков.
func NewMachine(d MachineDTO) (Machine, error) {
	for _, f := range []*string{&d.ID, &d.Name, &d.Group, &d.Host, &d.User, &d.KeyPath, &d.Jump, &d.TokenHint, &d.Note} {
		*f = strings.TrimSpace(*f)
	}
	switch {
	case d.Name == "":
		return Machine{}, fmt.Errorf("%w: нет имени", ErrInvalidMachine)
	case d.Host == "":
		return Machine{}, fmt.Errorf("%w: нет хоста", ErrInvalidMachine)
	case d.User == "":
		return Machine{}, fmt.Errorf("%w: нет пользователя", ErrInvalidMachine)
	}
	if !sshHost.MatchString(d.Host) {
		return Machine{}, fmt.Errorf("%w: хост %q — имя или адрес без пробелов, не с дефиса", ErrInvalidMachine, d.Host)
	}
	if !sshUser.MatchString(d.User) {
		return Machine{}, fmt.Errorf("%w: пользователь %q — буквы, цифры, точка, дефис и подчеркивание, не с дефиса", ErrInvalidMachine, d.User)
	}
	if d.Port == 0 {
		d.Port = defaultSSHPort
	}
	if d.Port < 1 || d.Port > 65535 {
		return Machine{}, fmt.Errorf("%w: порт %d — от 1 до 65535", ErrInvalidMachine, d.Port)
	}
	if d.Auth == "" {
		d.Auth = AuthKey
	}
	auth, err := ParseAuth(string(d.Auth))
	if err != nil {
		return Machine{}, err
	}
	d.Auth = auth
	if d.Env != "" {
		env, err := ParseEnv(string(d.Env))
		if err != nil {
			return Machine{}, fmt.Errorf("%w: %w", ErrInvalidMachine, err)
		}
		d.Env = env
	}
	if d.ID == "" {
		d.ID = slug(d.Name)
	}
	d.ID = strings.ToLower(d.ID)
	d.Jump = strings.ToLower(d.Jump)
	if !credentialID.MatchString(d.ID) {
		return Machine{}, fmt.Errorf("%w: id %q — латиница, цифры и дефис, до 64 знаков", ErrInvalidMachine, d.ID)
	}
	d.Projects = slices.Clone(d.Projects)
	for i := range d.Projects {
		p := &d.Projects[i]
		p.Name, p.Path, p.Port, p.What = strings.TrimSpace(p.Name), strings.TrimSpace(p.Path), strings.TrimSpace(p.Port), strings.TrimSpace(p.What)
		if p.Name == "" {
			return Machine{}, fmt.Errorf("%w: проект %d без имени", ErrInvalidMachine, i+1)
		}
	}
	return Machine{d: d}, nil
}

// ID is the stable name of the machine: build-01.
func (m Machine) ID() string { return m.d.ID }

// Name is what the machine is called.
func (m Machine) Name() string { return m.d.Name }

// Group is the folder the machine sits in.
func (m Machine) Group() string { return m.d.Group }

// Env is the environment; пусто — не задана.
func (m Machine) Env() Env { return m.d.Env }

// Host is the server address.
func (m Machine) Host() string { return m.d.Host }

// Port is the ssh port.
func (m Machine) Port() int { return m.d.Port }

// User is the ssh user.
func (m Machine) User() string { return m.d.User }

// Auth is how to sign in.
func (m Machine) Auth() Auth { return m.d.Auth }

// KeyPath is the key file path; самого ключа в карточке нет.
func (m Machine) KeyPath() string { return m.d.KeyPath }

// Jump is the id of the bastion machine; пусто — напрямую.
func (m Machine) Jump() string { return m.d.Jump }

// TokenHint says where to get a one-time certificate or token.
func (m Machine) TokenHint() string { return m.d.TokenHint }

// Note is a free remark.
func (m Machine) Note() string { return m.d.Note }

// Projects is what lies on the machine.
func (m Machine) Projects() []MachineProject { return slices.Clone(m.d.Projects) }

// DTO returns the card for storage and display.
func (m Machine) DTO() MachineDTO {
	d := m.d
	d.Projects = slices.Clone(d.Projects)
	return d
}

// Matches reports whether every word of q is somewhere in the card, без
// регистра; пустой запрос подходит всем. Поиск смотрит имя, группу, среду, хост,
// пользователя и проекты. Заметку, путь ключа и подсказку про токен не смотрит:
// число найденного у агента выдавало бы их содержимое по подстроке.
func (m Machine) Matches(q string) bool {
	d := m.d
	parts := []string{d.ID, d.Name, d.Group, string(d.Env), envSay[d.Env], d.Host, d.User}
	for _, p := range d.Projects {
		parts = append(parts, p.Name, p.Path, p.Port, p.What)
	}
	text := strings.ToLower(strings.Join(parts, " "))
	for w := range strings.FieldsSeq(strings.ToLower(q)) {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

// ResolveChain collects the bastions of m from its own cards: от внешнего
// бастиона к цели, цель последняя. Нет машины или петля — ErrMachineChain с
// именем, а не молча сломанная команда.
func ResolveChain(m Machine, byID func(id string) (Machine, bool)) ([]Machine, error) {
	chain := []Machine{m}
	seen := map[string]bool{m.ID(): true}
	for next := m.Jump(); next != ""; {
		if seen[next] {
			return nil, fmt.Errorf("%w: петля через %q", ErrMachineChain, next)
		}
		seen[next] = true
		j, ok := byID(next)
		if !ok {
			return nil, fmt.Errorf("%w: нет машины %q (бастион для %q)", ErrMachineChain, next, chain[0].ID())
		}
		chain = append([]Machine{j}, chain...)
		next = j.Jump()
	}
	return chain, nil
}

// SSHCommand builds the ssh command for a chain from ResolveChain: бастионы в
// -J, ключ и порт цели. ponytail: ключи бастионов в -J не передать, для них
// ключи берутся из ssh-agent или ssh_config; потолок, пока не понадобится
// ProxyCommand с -i на каждом звене.
func SSHCommand(chain []Machine) string {
	if len(chain) == 0 {
		return ""
	}
	target, hops := chain[len(chain)-1], chain[:len(chain)-1]
	args := []string{"ssh"}
	if len(hops) > 0 {
		via := make([]string, len(hops))
		for i, h := range hops {
			via[i] = h.User() + "@" + h.Host() + ":" + strconv.Itoa(h.Port())
		}
		args = append(args, "-J", strings.Join(via, ","))
	}
	if target.Auth() == AuthKey && target.KeyPath() != "" {
		args = append(args, "-i", shellPath(target.KeyPath()))
	}
	return strings.Join(append(args, "-p", strconv.Itoa(target.Port()), target.User()+"@"+target.Host()), " ")
}

// shellPath quotes a path for copy-paste into a shell, оставляя ~/ вне кавычек:
// в кавычках оболочка тильду не раскрывает.
func shellPath(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return "~/" + shellQuote(rest)
	}
	return shellQuote(p)
}

// shellQuote leaves a safe word as is and wraps anything else in single quotes.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// MachineAgentView is a card for the agent. Ни пути ключа, ни подсказки про
// токен, ни заметки: все, что получает агент, уходит провайдеру модели.
type MachineAgentView struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Group    string           `json:"group,omitempty"`
	Env      string           `json:"env,omitempty"`
	Host     string           `json:"host"`
	Port     int              `json:"port"`
	User     string           `json:"user"`
	Auth     string           `json:"auth"`
	Jump     string           `json:"jump,omitempty"`
	Projects []MachineProject `json:"projects,omitempty"`
}

// AgentView is the card as the agent may see it.
func (m Machine) AgentView() MachineAgentView {
	d := m.d
	return MachineAgentView{ID: d.ID, Name: d.Name, Group: d.Group, Env: string(d.Env), Host: d.Host, Port: d.Port,
		User: d.User, Auth: string(d.Auth), Jump: d.Jump, Projects: slices.Clone(d.Projects)}
}
