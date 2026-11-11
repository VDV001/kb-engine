package machines

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/daniil/kb-engine/internal/domain"
)

// Скан машины (#549): один раз после выдачи доступа список проектов на машине
// заполняется сам. По ssh выполняется ТОЛЬКО чтение, одной командой; та же
// команда и ее разбивка на шаги (ScanPlan) показываются человеку до запуска.
// Пароль в команды не подставляется: вход по ключу или агенту ssh, без вопросов
// (BatchMode), а неизвестный ключ хоста — отказ, а не молчаливое согласие.

// ScanCommand is the one read-only command run on the machine. Разделы ##name
// разбирает ParseScan; нет программы на машине — раздел пуст, не ошибка.
const ScanCommand = "echo '##docker'; docker ps --format '{{.Names}}\t{{.Ports}}' 2>/dev/null; " +
	"echo '##systemd'; systemctl list-units --type=service --state=running --no-legend --plain 2>/dev/null; " +
	"echo '##ports'; ss -ltn 2>/dev/null; " +
	"echo '##srv'; ls -1 /srv 2>/dev/null; " +
	"echo '##nginx'; nginx -T 2>/dev/null | grep -E '^[[:space:]]*server_name'"

// ScanPlan is what ScanCommand does, step by step, for the human to read.
func ScanPlan() []string {
	return []string{
		`docker ps --format "{{.Names}} {{.Ports}}"`,
		"systemctl list-units --type=service --state=running",
		"ss -ltn",
		"ls /srv",
		"nginx -T | grep server_name",
	}
}

// ErrScanNeedsKey — скан по паролю невозможен: ssh не принимает пароль без вопросов.
var ErrScanNeedsKey = errors.New("скан работает по ключу или агенту ssh, не по паролю")

// Runner runs a command on a machine over ssh; args — SSHArgs цепочки.
type Runner interface {
	Run(ctx context.Context, args []string, remote string) (string, error)
}

// Found is something the scan saw on the machine.
type Found struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Port   string `json:"port,omitempty"`
	What   string `json:"what"`
	Source string `json:"source"`
	New    bool   `json:"new"` // false — в карточке уже есть (по имени или пути)
}

// Scan reads what lies on the machine id through run. Вход по паролю, нет
// машины или разрыв цепочки — отказ до запуска ssh; ошибка ssh наружу без
// вывода машины.
func (s Service) Scan(ctx context.Context, id string, run Runner) ([]Found, error) {
	list, err := s.Machines.Load()
	if err != nil {
		return nil, err
	}
	i := indexOf(list, id)
	if i < 0 {
		return nil, fmt.Errorf("%w: %s", domain.ErrMachineNotFound, id)
	}
	if list[i].Auth() == domain.AuthPassword {
		return nil, fmt.Errorf("%w: %s", ErrScanNeedsKey, id)
	}
	chain, err := domain.ResolveChain(list[i], func(id string) (domain.Machine, bool) {
		j := indexOf(list, id)
		if j < 0 {
			return domain.Machine{}, false
		}
		return list[j], true
	})
	if err != nil {
		return nil, err
	}
	out, err := run.Run(ctx, domain.SSHArgs(chain), ScanCommand)
	if err != nil {
		return nil, fmt.Errorf("скан %s: %w", id, err)
	}
	found := ParseScan(out)
	have := list[i].Projects()
	for k := range found {
		found[k].New = !slicesContainsProject(have, found[k])
	}
	return found, nil
}

func slicesContainsProject(have []domain.MachineProject, f Found) bool {
	for _, p := range have {
		if strings.EqualFold(p.Name, f.Name) || (f.Path != "" && p.Path == f.Path) {
			return true
		}
	}
	return false
}

// ponytail: списки шума — эвристика: службы и порты, которые есть на любой
// машине и проектами не считаются. Потолок: чужой дистрибутив с другими
// именами покажет лишнее; путь — вынести списки в настройки.
var (
	noiseServices = map[string]bool{"nginx": true, "ssh": true, "sshd": true, "docker": true, "containerd": true, "cron": true,
		"rsyslog": true, "dbus": true, "polkit": true, "networkd-dispatcher": true, "unattended-upgrades": true, "snapd": true,
		"irqbalance": true, "multipathd": true, "chrony": true, "ntp": true, "fail2ban": true, "qemu-guest-agent": true}
	noisePorts = map[string]bool{"22": true, "53": true}
	dockerPort = regexp.MustCompile(`:(\d+)->`)
)

// ParseScan turns the output of ScanCommand into what lies on the machine.
func ParseScan(out string) []Found {
	sections := map[string][]string{}
	cur := ""
	for line := range strings.SplitSeq(out, "\n") {
		if name, ok := strings.CutPrefix(line, "##"); ok {
			cur = strings.TrimSpace(name)
			continue
		}
		if strings.TrimSpace(line) != "" {
			sections[cur] = append(sections[cur], line)
		}
	}
	docker, named := parseDocker(sections["docker"])
	found := append(docker, parseSystemd(sections["systemd"])...)
	found = append(found, parseNginx(sections["nginx"])...)
	found = append(found, parseSrv(sections["srv"])...)
	return append(found, parsePorts(sections["ports"], named)...)
}

// parseDocker reads «имя<tab>порты»; named — порты, которые контейнеры уже назвали.
func parseDocker(lines []string) (found []Found, named map[string]bool) {
	named = map[string]bool{}
	for _, l := range lines {
		name, ports, _ := strings.Cut(l, "\t")
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		f := Found{Name: name, What: "контейнер docker", Source: "docker"}
		if m := dockerPort.FindStringSubmatch(ports); m != nil {
			f.Port = m[1]
			named[m[1]] = true
		}
		found = append(found, f)
	}
	return found, named
}

func parseSystemd(lines []string) (found []Found) {
	for _, l := range lines {
		unit := strings.TrimSuffix(strings.Fields(l)[0], ".service")
		if noiseServices[unit] || strings.HasPrefix(unit, "systemd-") || strings.HasPrefix(unit, "getty") || strings.HasPrefix(unit, "user@") {
			continue
		}
		found = append(found, Found{Name: unit, What: "служба systemd, запущена", Source: "systemd"})
	}
	return found
}

func parseNginx(lines []string) (found []Found) {
	for _, l := range lines {
		_, names, _ := strings.Cut(l, "server_name")
		for n := range strings.FieldsSeq(strings.TrimSuffix(strings.TrimSpace(names), ";")) {
			if n != "_" && n != "localhost" && !strings.HasPrefix(n, "$") {
				found = append(found, Found{Name: n, What: "виртуальный хост nginx", Source: "nginx"})
			}
		}
	}
	return found
}

func parseSrv(lines []string) (found []Found) {
	for _, l := range lines {
		if n := strings.TrimSpace(l); n != "" && !strings.HasPrefix(n, ".") && !strings.Contains(n, "/") {
			found = append(found, Found{Name: n, Path: "/srv/" + n, What: "каталог в /srv", Source: "каталог /srv"})
		}
	}
	return found
}

// parsePorts reads «ss -ltn»; порты, которые назвал docker, и служебные не повторяются.
func parsePorts(lines []string, named map[string]bool) (found []Found) {
	seen := map[string]bool{}
	for _, l := range lines {
		fs := strings.Fields(l)
		if len(fs) < 4 || fs[0] != "LISTEN" {
			continue
		}
		port := fs[3][strings.LastIndex(fs[3], ":")+1:]
		if noisePorts[port] || named[port] || seen[port] {
			continue
		}
		seen[port] = true
		found = append(found, Found{Name: "порт " + port, Port: port, What: "слушает порт", Source: "порт"})
	}
	return found
}
