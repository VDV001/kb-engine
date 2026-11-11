package machines_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/daniil/kb-engine/internal/domain"
	"github.com/daniil/kb-engine/internal/usecase/machines"
)

// runnerFake stands in for ssh: out — что вернула машина, err — ssh не дошел.
type runnerFake struct {
	out    string
	err    error
	args   []string
	remote string
}

func (r *runnerFake) Run(_ context.Context, args []string, remote string) (string, error) {
	r.args, r.remote = args, remote
	return r.out, r.err
}

// Вывод построен по разделам команды скана; имена и порты выдуманные.
const scanOut = "##docker\n" +
	"api\t0.0.0.0:3000->3000/tcp, :::3000->3000/tcp\n" +
	"queue\t\n" +
	"##systemd\n" +
	"worker.service loaded active running Фоновые задачи\n" +
	"nginx.service loaded active running A high performance web server\n" +
	"##ports\n" +
	"State  Recv-Q Send-Q Local Address:Port Peer Address:Port\n" +
	"LISTEN 0      4096         0.0.0.0:3000      0.0.0.0:*\n" +
	"LISTEN 0      4096         0.0.0.0:5432      0.0.0.0:*\n" +
	"LISTEN 0      128             [::]:22           [::]:*\n" +
	"##srv\n" +
	"проект-а\n" +
	"d\n" +
	"##nginx\n" +
	"    server_name site.example.test www.site.example.test;\n"

// Скан запускает ssh по цепочке карточки и одну команду только на чтение, а
// найденное раскладывает: что есть в карточке, помечено не новым.
func TestService_Scan(t *testing.T) {
	s, _ := seeded(t)
	run := &runnerFake{out: scanOut}
	found, err := s.Scan(context.Background(), "build-01", run)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if want := []string{"-J", "ops@203.0.113.7:22", "-p", "22", "ops@10.0.0.5"}; !slices.Equal(run.args, want) {
		t.Errorf("аргументы ssh:\n got %q\nwant %q", run.args, want)
	}
	if run.remote != machines.ScanCommand {
		t.Errorf("команда на машине не та, что показана человеку")
	}

	byName := map[string]machines.Found{}
	for _, f := range found {
		byName[f.Name] = f
	}
	for _, c := range []struct {
		name, path, port, source string
		isNew                    bool
	}{
		{"api", "", "3000", "docker", true},
		{"queue", "", "", "docker", true},
		{"worker", "", "", "systemd", true},
		{"проект-а", "/srv/проект-а", "", "каталог /srv", false}, // уже в карточке
		{"d", "/srv/d", "", "каталог /srv", true},
		{"site.example.test", "", "", "nginx", true},
		{"www.site.example.test", "", "", "nginx", true},
		{"порт 5432", "", "5432", "порт", true},
	} {
		f, ok := byName[c.name]
		if !ok {
			t.Errorf("не найдено %q; есть %v", c.name, names(found))
			continue
		}
		if f.Path != c.path || f.Port != c.port || f.Source != c.source || f.New != c.isNew {
			t.Errorf("%q = %+v, нужно путь %q порт %q источник %q новое %v", c.name, f, c.path, c.port, c.source, c.isNew)
		}
	}
	// Сам nginx как служба и порты 22 и 3000 (уже названы docker) шумом не идут.
	for _, noise := range []string{"nginx", "порт 3000", "порт 22"} {
		if _, ok := byName[noise]; ok {
			t.Errorf("шум в результате: %q", noise)
		}
	}
}

// Пустые разделы (команды нет на машине) — не ошибка, а пустой результат.
func TestService_Scan_emptySections(t *testing.T) {
	s, _ := seeded(t)
	found, err := s.Scan(context.Background(), "build-01", &runnerFake{out: "##docker\n##systemd\n##ports\n##srv\n##nginx\n"})
	if err != nil || len(found) != 0 {
		t.Fatalf("пустая машина: %v, %v", found, err)
	}
}

func TestService_Scan_rejects(t *testing.T) {
	s, st := seeded(t)
	pw := st.list[1].DTO()
	pw.Auth = domain.AuthPassword
	if err := s.Update("build-01", pw); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("edge-02"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		id   string
		is   error
	}{
		{"нет такой машины", "нет-такой", domain.ErrMachineNotFound},
		{"вход по паролю", "build-01", machines.ErrScanNeedsKey},
		{"разрыв цепочки", "db-03", domain.ErrMachineChain}, // бастион edge-02 удален
	} {
		t.Run(c.name, func(t *testing.T) {
			run := &runnerFake{}
			if _, err := s.Scan(context.Background(), c.id, run); !errors.Is(err, c.is) {
				t.Fatalf("ошибка: %v", err)
			}
			if run.remote != "" {
				t.Fatal("ssh запущен для отказанной машины")
			}
		})
	}
	// ssh не дошел: ошибка наружу, содержимое вывода в нее не попадает.
	s2, _ := seeded(t)
	_, err := s2.Scan(context.Background(), "edge-02", &runnerFake{out: "секрет-в-выводе", err: errors.New("Host key verification failed")})
	if err == nil || strings.Contains(err.Error(), "секрет-в-выводе") {
		t.Fatalf("ошибка ssh: %v", err)
	}
}

func names(f []machines.Found) []string {
	out := make([]string, len(f))
	for i, x := range f {
		out[i] = x.Name
	}
	return out
}
